package bambulan

import (
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
		ftp.WithTimeout(10*time.Second), // Increased timeout for stability
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

// ListFiles returns a detailed list of files in the specified directory.
//
// Parameters:
//   - dir: The remote directory path to list (e.g., "/timelapse").
//
// Returns:
//   - A slice of `*ftp.Entry`, each containing file/directory information.
//
// Example:
//
//	entries, err := client.File.ListFiles("/timelapse")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, entry := range entries {
//	    fmt.Printf("%s: %d bytes\n", entry.Name, entry.Size)
//	}
func (f *FileClient) ListFiles(dir string) ([]*ftp.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return nil, err
	}

	entries, err := c.List(dir)
	if err != nil {
		// Invalidate connection on error to force clean slate next time
		_ = c.Quit()
		f.client = nil
		return nil, err
	}
	return entries, nil
}

// GetFiles returns a list of file names in the specified directory that match the given extension.
// Note: The Bambu printer's FTPS server does not support globbing, so this method filters results client-side.
//
// Parameters:
//   - dir: The remote directory path to search (e.g., "/timelapse").
//   - extension: The file extension to match (e.g., ".3mf", ".mp4").
//
// Returns:
//   - A slice of strings, where each string is the name of a matching file.
func (f *FileClient) GetFiles(dir string, extension string) ([]string, error) {
	// The Bambu MCU FTPS server can't glob.
	entries, err := f.ListFiles(dir)
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
//
// Parameters:
//   - remotePath: The full path to the file on the printer (e.g., "/timelapse/video.mp4").
//
// Returns:
//   - An `io.ReadCloser` from which the file content can be read.
func (f *FileClient) Download(remotePath string) (io.ReadCloser, error) {
	f.mu.Lock()

	c, err := f.getConn()
	if err != nil {
		f.mu.Unlock() // Unlock if error
		return nil, err
	}

	// Create a pipe to stream the data
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		// If Retrieve fails, the writer is closed with error, reader sees error.
		if err := c.Retrieve(remotePath, pw); err != nil {
			pw.CloseWithError(err)
		}
	}()

	return &ftpReadCloser{ReadCloser: pr, client: f}, nil
}

type ftpReadCloser struct {
	io.ReadCloser
	client *FileClient
}

func (f *ftpReadCloser) Close() error {
	err := f.ReadCloser.Close()
	// Release the lock, allowing other operations to proceed
	f.client.mu.Unlock()
	return err
}

// DownloadFile downloads a file from the printer to a local path.
//
// Parameters:
//   - remotePath: The full path to the file on the printer.
//   - localPath: The local file system path where the file should be saved.
//   - onProgress: An optional callback function `func(currentBytes, totalBytes int64)`
//     that reports the current download progress. `totalBytes` will be 0 if unknown.
//
// Example:
//
//	err := client.File.DownloadFile("/timelapse/video.mp4", "./video.mp4", func(current, total int64) {
//	    if total > 0 {
//	        fmt.Printf("Downloading: %.1f%%\r", float64(current)/float64(total)*100)
//	    }
//	})
func (f *FileClient) DownloadFile(remotePath, localPath string, onProgress func(int64, int64)) error {
	return f.downloadFileInternal(remotePath, localPath, onProgress)
}

// DownloadDirectory downloads a remote directory to a local directory.
//
// Parameters:
//   - remoteDir: The remote directory path to download.
//   - localDir: The local directory path where files should be saved.
//   - recursive: If true, downloads subdirectories recursively.
//   - onProgress: An optional callback function `func(filename string, current, total int64)`
//     that reports progress for each file.
func (f *FileClient) DownloadDirectory(remoteDir, localDir string, recursive bool, onProgress func(string, int64, int64)) error {
	// Ensure local directory exists
	info, err := os.Stat(localDir)
	if err != nil {
		return fmt.Errorf("local directory error: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local path %s is not a directory", localDir)
	}

	entries, err := f.ListFiles(remoteDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}

		remotePath := path.Join(remoteDir, entry.Name)
		localPath := filepath.Join(localDir, entry.Name)

		if entry.Type == "dir" {
			if recursive {
				if err := os.MkdirAll(localPath, 0755); err != nil {
					return err
				}
				if err := f.DownloadDirectory(remotePath, localPath, recursive, onProgress); err != nil {
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
			if err := f.DownloadFile(remotePath, localPath, fileProgress); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *FileClient) downloadFileInternal(remotePath, localPath string, onProgress func(int64, int64)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}

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
		// Wrap the writer with progress tracking
		pw := &progressWriter{
			Writer:     outFile,
			total:      total,
			onProgress: onProgress,
		}
		if err := c.Retrieve(remotePath, pw); err != nil {
			_ = c.Quit()
			f.client = nil
			return err
		}
	} else {
		if err := c.Retrieve(remotePath, outFile); err != nil {
			_ = c.Quit()
			f.client = nil
			return err
		}
	}
	return nil
}

// Upload streams content to the printer.
//
// Parameters:
//   - remotePath: The full path where the file should be saved on the printer.
//   - content: An `io.Reader` providing the content to upload.
//   - onProgress: An optional callback function `func(currentBytes, totalBytes int64)`
//     that reports the current upload progress.
func (f *FileClient) Upload(remotePath string, content io.Reader, onProgress func(int64, int64)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}

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

	if err := c.Store(remotePath, reader); err != nil {
		_ = c.Quit()
		f.client = nil
		return err
	}
	return nil
}

// UploadFile uploads a local file to the printer.
//
// Parameters:
//   - localPath: The local file system path of the file to upload.
//   - remotePath: The full path where the file should be saved on the printer.
//   - onProgress: An optional callback function `func(currentBytes, totalBytes int64)`
//     that reports the current upload progress.
//
// Example:
//
//	err := client.File.UploadFile("./model.gcode.3mf", "/model.gcode.3mf", nil)
func (f *FileClient) UploadFile(localPath, remotePath string, onProgress func(int64, int64)) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file

	return f.Upload(remotePath, reader, onProgress)
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
//   - remotePath: The full absolute path to the file on the printer (e.g., "/timelapse/video.mp4").
//
// Returns:
//   - An error if the file could not be deleted (e.g., file not found, permission denied).
func (f *FileClient) Delete(remotePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}

	if err := c.Delete(remotePath); err != nil {
		// Invalidate on error
		_ = c.Quit()
		f.client = nil
		return err
	}
	return nil
}

// MakeDirectory creates a new directory on the printer.
//
// Parameters:
//   - path: The full absolute path of the directory to create.
//
// Returns:
//   - An error if the directory could not be created.
func (f *FileClient) MakeDirectory(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}

	if err := c.MakeDir(path); err != nil {
		_ = c.Quit()
		f.client = nil
		return err
	}
	return nil
}

// Rename renames or moves a file/directory on the printer.
//
// Parameters:
//   - source: The current full path of the file or directory.
//   - dest: The new full path (including name) for the file or directory.
//
// Returns:
//   - An error if the operation failed.
func (f *FileClient) Rename(source, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}

	if err := c.Rename(source, dest); err != nil {
		_ = c.Quit()
		f.client = nil
		return err
	}
	return nil
}

// RemoveAll recursively deletes a file or directory.
// If the path is a directory, it deletes all its contents before deleting the directory itself.
//
// Parameters:
//   - path: The full absolute path to remove.
//
// Returns:
//   - An error if any deletion step failed.
func (f *FileClient) RemoveAll(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.getConn()
	if err != nil {
		return err
	}

	if err := f.removeAll(c, path); err != nil {
		_ = c.Quit()
		f.client = nil
		return err
	}
	return nil
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
