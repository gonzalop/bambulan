package bambulan

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/gonzalop/ftp"
)

// FileClient handles file operations (listing, uploading, downloading) over FTPS.
// It maintains a persistent connection to the printer.
type FileClient struct {
	// Hostname is the IP or hostname of the printer's FTPS server.
	Hostname string
	// AccessCode is the password for the FTPS connection.
	AccessCode string

	// client tracks the active FTP connection
	client *ftp.Client
	// mu ensures thread-safety for the shared connection
	mu sync.Mutex
}

// NewFileClient creates a new FileClient.
//
// Parameters:
//   - hostname: The IP address or hostname of the printer's FTPS server.
//   - accessCode: The printer's access code, usually found in the Network settings.
func NewFileClient(hostname, accessCode string) *FileClient {
	return &FileClient{
		Hostname:   hostname,
		AccessCode: accessCode,
	}
}

// Close closes the active FTP connection if it exists.
func (f *FileClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.client != nil {
		err := f.client.Quit()
		f.client = nil
		return err
	}
	return nil
}

// getConn returns a usable FTP connection, reconnecting if necessary.
// The caller must hold f.mu.
func (f *FileClient) getConn() (*ftp.Client, error) {
	// Check existing connection
	if f.client != nil {
		if err := f.client.Noop(); err == nil {
			return f.client, nil
		}
		// Connection dead, close and retry
		slog.Debug("FTP connection lost, reconnecting", "host", f.Hostname)
		_ = f.client.Quit()
		f.client = nil
	}

	// Dial new connection
	tlsConfig := &tls.Config{
		// Bambu Lab printers use self-signed certificates for their FTPS server.
		InsecureSkipVerify: true,
	}

	c, err := ftp.Dial(fmt.Sprintf("%s:990", f.Hostname),
		ftp.WithImplicitTLS(tlsConfig),
		ftp.WithTimeout(10*time.Second),
		ftp.WithLogger(slog.Default()),
	)
	if err != nil {
		return nil, err
	}

	if err := c.Login("bblp", f.AccessCode); err != nil {
		_ = c.Quit()
		return nil, err
	}

	f.client = c
	return c, nil
}

// withContext runs fn against the shared FTP connection while holding f.mu.
// If ctx is cancelled while fn is blocking, Quit() is called on the connection
// to unblock it immediately. The connection is then marked for reconnection so
// the next operation gets a fresh, clean session via getConn().
func (f *FileClient) withContext(ctx context.Context, fn func(*ftp.Client) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Run the blocking FTP call in a goroutine so we can select on ctx.
	errCh := make(chan error, 1)
	go func() { errCh <- fn(c) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		_ = c.Quit()   // unblocks fn's goroutine by closing the TCP connection
		f.client = nil // force a fresh reconnect on next call
		<-errCh        // drain — fn returns an error from the now-closed conn
		return ctx.Err()
	}
}

// ListFiles returns a detailed list of files in the specified directory.
//
// Parameters:
//   - ctx: Context for cancellation. If cancelled, the FTP connection is closed.
//   - dir: The remote directory path to list (e.g., "/timelapse").
//
// Returns:
//   - A slice of `*ftp.Entry`, each containing file/directory information.
//
// Example:
//
//	entries, err := client.File.ListFiles(ctx, "/timelapse")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, entry := range entries {
//	    fmt.Printf("%s: %d bytes\n", entry.Name, entry.Size)
//	}
func (f *FileClient) ListFiles(ctx context.Context, dir string) ([]*ftp.Entry, error) {
	var entries []*ftp.Entry
	err := f.withContext(ctx, func(c *ftp.Client) error {
		var e error
		entries, e = c.List(dir)
		return e
	})
	return entries, err
}

// GetFiles returns a list of file names in the specified directory that match the given extension.
// Note: The Bambu printer's FTPS server does not support globbing, so this method filters results client-side.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - dir: The remote directory path to search (e.g., "/timelapse").
//   - extension: The file extension to match (e.g., ".3mf", ".mp4").
//
// Returns:
//   - A slice of strings, where each string is the name of a matching file.
func (f *FileClient) GetFiles(ctx context.Context, dir string, extension string) ([]string, error) {
	// The Bambu MCU FTPS server can't glob.
	entries, err := f.ListFiles(ctx, dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.Type == "file" && filepath.Ext(entry.Name) == extension {
			files = append(files, entry.Name)
		}
	}
	return files, nil
}

// Download streams a file from the printer.
// The caller is responsible for closing the returned `io.ReadCloser`.
// If ctx is cancelled, the dedicated download connection is closed and the reader will return an error.
//
// Parameters:
//   - ctx: Context for cancellation. Cancellation closes the underlying connection.
//   - remotePath: The full path to the file on the printer (e.g., "/timelapse/video.mp4").
//
// Returns:
//   - An `io.ReadCloser` from which the file content can be read.
func (f *FileClient) Download(ctx context.Context, remotePath string) (io.ReadCloser, error) {
	// For streaming, we dial a dedicated connection to avoid blocking the shared
	// client mutex for the duration of the download.
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	c, err := ftp.Dial(fmt.Sprintf("%s:990", f.Hostname),
		ftp.WithImplicitTLS(tlsConfig),
		ftp.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, err
	}

	if err := c.Login("bblp", f.AccessCode); err != nil {
		_ = c.Quit()
		return nil, err
	}

	// If ctx is cancelled, close the dedicated connection to abort the in-flight transfer.
	context.AfterFunc(ctx, func() { _ = c.Quit() })

	// Create a pipe to stream the data
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer func() { _ = c.Quit() }()

		// If Retrieve fails, the writer is closed with error, reader sees error.
		if err := c.Retrieve(remotePath, pw); err != nil {
			pw.CloseWithError(err)
		}
	}()

	return pr, nil
}

// DownloadFile downloads a file from the printer to a local path.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - remotePath: The full path to the file on the printer.
//   - localPath: The local file system path where the file should be saved.
//   - onProgress: An optional callback function `func(currentBytes, totalBytes int64)`
//     that reports the current download progress. `totalBytes` will be 0 if unknown.
//
// Example:
//
//	err := client.File.DownloadFile(ctx, "/timelapse/video.mp4", "./video.mp4", func(current, total int64) {
//	    if total > 0 {
//	        fmt.Printf("Downloading: %.1f%%\r", float64(current)/float64(total)*100)
//	    }
//	})
func (f *FileClient) DownloadFile(ctx context.Context, remotePath, localPath string, onProgress func(int64, int64)) error {
	return f.downloadFileInternal(ctx, remotePath, localPath, onProgress)
}

// DownloadDirectory downloads a remote directory to a local directory.
//
// Parameters:
//   - ctx: Context for cancellation. Checked between files; an in-progress file download will be cancelled too.
//   - remoteDir: The remote directory path to download.
//   - localDir: The local directory path where files should be saved.
//   - recursive: If true, downloads subdirectories recursively.
//   - onProgress: An optional callback function `func(filename string, current, total int64)`
//     that reports progress for each file.
func (f *FileClient) DownloadDirectory(ctx context.Context, remoteDir, localDir string, recursive bool, onProgress func(string, int64, int64)) error {
	// Ensure local directory exists
	info, err := os.Stat(localDir)
	if err != nil {
		return fmt.Errorf("local directory error: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local path %s is not a directory", localDir)
	}

	entries, err := f.ListFiles(ctx, remoteDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		remotePath := path.Join(remoteDir, entry.Name)
		localPath := filepath.Join(localDir, entry.Name)

		if entry.Type == "dir" {
			if recursive {
				if err := os.MkdirAll(localPath, 0755); err != nil {
					return err
				}
				if err := f.DownloadDirectory(ctx, remotePath, localPath, recursive, onProgress); err != nil {
					return err
				}
			}
		} else {
			// It's a file
			var fileProgress func(int64, int64)
			if onProgress != nil {
				name := entry.Name
				fileProgress = func(current, total int64) {
					onProgress(name, current, total)
				}
			}
			if err := f.DownloadFile(ctx, remotePath, localPath, fileProgress); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *FileClient) downloadFileInternal(ctx context.Context, remotePath, localPath string, onProgress func(int64, int64)) error {
	return f.withContext(ctx, func(c *ftp.Client) error {
		// Try to get size first
		var total int64
		if size, err := c.Size(remotePath); err == nil {
			total = size
		}

		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return err
		}

		outFile, err := os.Create(localPath)
		if err != nil {
			return err
		}
		defer outFile.Close()

		if onProgress != nil {
			pw := &progressWriter{
				Writer:     outFile,
				total:      total,
				onProgress: onProgress,
			}
			return c.Retrieve(remotePath, pw)
		}
		return c.Retrieve(remotePath, outFile)
	})
}

// Upload streams content to the printer.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - remotePath: The full path where the file should be saved on the printer.
//   - content: An `io.Reader` providing the content to upload.
//   - onProgress: An optional callback function `func(currentBytes, totalBytes int64)`
//     that reports the current upload progress.
func (f *FileClient) Upload(ctx context.Context, remotePath string, content io.Reader, onProgress func(int64, int64)) error {
	return f.withContext(ctx, func(c *ftp.Client) error {
		reader := content
		if onProgress != nil {
			// Try to see if we can get the total size
			var total int64
			if seeker, ok := content.(io.Seeker); ok {
				// Save current pos
				current, err := seeker.Seek(0, io.SeekCurrent)
				if err == nil {
					// Go to end
					size, err := seeker.Seek(0, io.SeekEnd)
					if err == nil {
						total = size
						_, _ = seeker.Seek(current, io.SeekStart)
					}
				}
			}

			reader = &progressReader{
				Reader:     content,
				total:      total,
				onProgress: onProgress,
			}
		}

		return c.Store(remotePath, reader)
	})
}

// UploadFile uploads a local file to the printer.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - localPath: The local file system path of the file to upload.
//   - remotePath: The full path where the file should be saved on the printer.
//   - onProgress: An optional callback function `func(currentBytes, totalBytes int64)`
//     that reports the current upload progress.
//
// Example:
//
//	err := client.File.UploadFile(ctx, "./model.gcode.3mf", "/model.gcode.3mf", nil)
func (f *FileClient) UploadFile(ctx context.Context, localPath, remotePath string, onProgress func(int64, int64)) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return f.Upload(ctx, remotePath, file, onProgress)
}

type progressReader struct {
	io.Reader
	current    int64
	total      int64
	onProgress func(int64, int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.current += int64(n)
	pr.onProgress(pr.current, pr.total)
	return n, err
}

type progressWriter struct {
	io.Writer
	current    int64
	total      int64
	onProgress func(int64, int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	pw.current += int64(n)
	pw.onProgress(pw.current, pw.total)
	return n, err
}

// Delete deletes a file from the printer.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - remotePath: The full absolute path to the file on the printer (e.g., "/timelapse/video.mp4").
//
// Returns:
//   - An error if the file could not be deleted (e.g., file not found, permission denied).
func (f *FileClient) Delete(ctx context.Context, remotePath string) error {
	return f.withContext(ctx, func(c *ftp.Client) error {
		return c.Delete(remotePath)
	})
}

// MakeDirectory creates a new directory on the printer.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - path: The full absolute path of the directory to create.
//
// Returns:
//   - An error if the directory could not be created.
func (f *FileClient) MakeDirectory(ctx context.Context, path string) error {
	return f.withContext(ctx, func(c *ftp.Client) error {
		return c.MakeDir(path)
	})
}

// Rename renames or moves a file/directory on the printer.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - source: The current full path of the file or directory.
//   - dest: The new full path (including name) for the file or directory.
//
// Returns:
//   - An error if the operation failed.
func (f *FileClient) Rename(ctx context.Context, source, dest string) error {
	return f.withContext(ctx, func(c *ftp.Client) error {
		return c.Rename(source, dest)
	})
}

// RemoveAll recursively deletes a file or directory.
// If the path is a directory, it deletes all its contents before deleting the directory itself.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - path: The full absolute path to remove.
//
// Returns:
//   - An error if any deletion step failed.
func (f *FileClient) RemoveAll(ctx context.Context, path string) error {
	return f.withContext(ctx, func(c *ftp.Client) error {
		return f.removeAll(c, path)
	})
}

func (f *FileClient) removeAll(c *ftp.Client, path string) error {
	// Try to list the path to see if it's a directory
	entries, err := c.List(path)
	if err != nil {
		// If listing fails, it might be a file or not exist
		// Try deleting as file
		if delErr := c.Delete(path); delErr == nil {
			return nil
		}
		// If delete failed, return the original list error or delete error?
		// Usually if list fails it means it's not a dir (550).
		return err
	}

	// It's a directory (or empty), iterate entries
	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		fullPath := filepath.Join(path, entry.Name)

		if entry.Type == "dir" {
			if err := f.removeAll(c, fullPath); err != nil {
				return err
			}
		} else {
			if err := c.Delete(fullPath); err != nil {
				return err
			}
		}
	}

	// usage of RemoveDir usually requires empty directory
	return c.RemoveDir(path)
}
