package bambulan

import (
	"crypto/tls"
	"fmt"
	"io"
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
	c, err := ftp.Dial(fmt.Sprintf("%s:990", f.Hostname), ftp.DialWithTimeout(5*time.Second), ftp.DialWithTLS(&tls.Config{
		InsecureSkipVerify: true,
	}))
	if err != nil {
		return nil, err
	}

	if err := c.Login("bblp", f.AccessCode); err != nil {
		_ = c.Quit()
		return nil, err
	}

	return c, nil
}

// GetFiles returns a list of files in the specified directory with the given extension.
func (f *FileClient) GetFiles(dir string, extension string) ([]string, error) {
	c, err := f.connect()
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Quit() }()

	entries, err := c.List(dir)
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

// DownloadFile downloads a file from the printer to a local path.
// onProgress: Optional callback for tracking download progress (current bytes, total bytes).
func (f *FileClient) DownloadFile(remotePath, localPath string, onProgress func(int64, int64)) error {
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

// UploadFile uploads a local file to the printer.
// onProgress: Optional callback for tracking upload progress (current bytes, total bytes).
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

	c, err := f.connect()
	if err != nil {
		return err
	}
	defer func() { _ = c.Quit() }()

	var reader io.Reader = file
	if onProgress != nil {
		reader = &progressReader{
			Reader:     file,
			total:      total,
			onProgress: onProgress,
		}
	}

	return c.Stor(remotePath, reader)
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
