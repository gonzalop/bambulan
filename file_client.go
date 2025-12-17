package bambulan

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jlaffaye/ftp"
)

// FileClient handles file operations (listing, uploading, downloading) over FTPS.
type FileClient struct {
	Hostname   string
	AccessCode string
}

// NewFileClient creates a new FileClient.
func NewFileClient(hostname, accessCode string) *FileClient {
	return &FileClient{
		Hostname:   hostname,
		AccessCode: accessCode,
	}
}

func (f *FileClient) connect() (*ftp.ServerConn, error) {
	// Use a custom dialer to intercept 0.0.0.0 addresses from the printer PASV
	// and replace them with the actual hostname.
	fixIPDialer := func(network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		// If PASV asks us to connect to 0.0.0.0...
		if host == "0.0.0.0" || host == "::" {
			slog.Info("Fixing invalid data address", "old", addr, "new_host", f.Hostname, "port", port)
			// Reassemble using the known good host and the new port
			addr = net.JoinHostPort(f.Hostname, port)
		}

		// Proceed with the standard dial using the fixed address
		slog.Debug("Dialing FTPS", "address", addr)
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		tlsConfig := &tls.Config{
			// Bambu Lab printers use self-signed certificates for their FTPS server.
			InsecureSkipVerify: true,
		}
		return tls.DialWithDialer(dialer, network, addr, tlsConfig)
	}

	c, err := ftp.Dial(fmt.Sprintf("%s:990", f.Hostname),
		// Note: We do NOT use ftp.DialWithTLS here because our custom dialer
		// already returns a TLS connection. Using both would cause double-wrapping.
		ftp.DialWithTimeout(5*time.Second),
		ftp.DialWithDebugOutput(os.Stdout),
		ftp.DialWithDialFunc(fixIPDialer),
	)
	if err != nil {
		return nil, err
	}

	if err := c.Login("bblp", f.AccessCode); err != nil {
		_ = c.Quit()
		return nil, err
	}

	return c, nil
}

// ListFiles returns a detailed list of files in the specified directory.
func (f *FileClient) ListFiles(dir string) ([]*ftp.Entry, error) {
	c, err := f.connect()
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Quit() }()

	entries, err := c.List(dir)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// GetFiles returns a list of files in the specified directory with the given extension.
func (f *FileClient) GetFiles(dir string, extension string) ([]string, error) {
	// The Bambu MCU FTPS server can't glob.
	entries, err := f.ListFiles(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.Type == ftp.EntryTypeFile && filepath.Ext(entry.Name) == extension {
			files = append(files, entry.Name)
		}
	}
	return files, nil
}

// Download streams a file from the printer.
// The caller is responsible for closing the returned ReadCloser.
func (f *FileClient) Download(remotePath string) (io.ReadCloser, error) {
	// Note: We can't defer c.Quit() here because the caller needs to read from the connection.
	// We wrap the ReadCloser to close the connection when done.
	c, err := f.connect()
	if err != nil {
		return nil, err
	}

	resp, err := c.Retr(remotePath)
	if err != nil {
		_ = c.Quit()
		return nil, err
	}

	return &ftpReadCloser{ReadCloser: resp, conn: c}, nil
}

type ftpReadCloser struct {
	io.ReadCloser
	conn *ftp.ServerConn
}

func (f *ftpReadCloser) Close() error {
	err := f.ReadCloser.Close()
	_ = f.conn.Quit()
	return err
}

// DownloadFile downloads a file from the printer to a local path.
// The onProgress callback, if not nil, reports the current downloaded bytes and total size.
func (f *FileClient) DownloadFile(remotePath, localPath string, onProgress func(int64, int64)) error {
	reader, err := f.Download(remotePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	return f.downloadFileInternal(remotePath, localPath, onProgress)
}

func (f *FileClient) downloadFileInternal(remotePath, localPath string, onProgress func(int64, int64)) error {
	c, err := f.connect()
	if err != nil {
		return err
	}
	defer func() { _ = c.Quit() }()

	// Try to get size first
	var total int64
	if size, err := c.FileSize(remotePath); err == nil {
		total = size
	}

	resp, err := c.Retr(remotePath)
	if err != nil {
		return err
	}
	defer resp.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}

	outFile, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	var reader io.Reader = resp
	if onProgress != nil {
		reader = &progressReader{
			Reader:     resp,
			total:      total,
			onProgress: onProgress,
		}
	}

	_, err = io.Copy(outFile, reader)
	return err
}

// Upload streams content to the printer.
func (f *FileClient) Upload(remotePath string, content io.Reader) error {
	c, err := f.connect()
	if err != nil {
		return err
	}
	defer func() { _ = c.Quit() }()

	return c.Stor(remotePath, content)
}

// UploadFile uploads a local file to the printer.
// The onProgress callback, if not nil, reports the current uploaded bytes and total size.
func (f *FileClient) UploadFile(localPath, remotePath string, onProgress func(int64, int64)) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	total := info.Size()

	var reader io.Reader = file
	if onProgress != nil {
		reader = &progressReader{
			Reader:     file,
			total:      total,
			onProgress: onProgress,
		}
	}

	return f.Upload(remotePath, reader)
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
