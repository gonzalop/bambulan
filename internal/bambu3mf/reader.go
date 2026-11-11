package bambu3mf

import (
	"archive/zip"
	"fmt"
	"io"
)

// Reader provides access to the contents of a Bambu Lab 3MF file.
type Reader struct {
	z           *zip.Reader
	closer      io.Closer
	MaxFileSize int64 // Maximum allowed size for a single file entry (default: 50MB)
}

// DefaultMaxFileSize is the default maximum size (50 MB) allowed when extracting a single file from a 3MF archive.
const DefaultMaxFileSize = 50 * 1024 * 1024

// Open opens a Bambu 3MF file for reading.
// It returns a Reader that can be used to extract metadata and thumbnails.
// The caller must call Close() when done.
func Open(filename string) (*Reader, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open 3mf file: %w", err)
	}
	return &Reader{z: &z.Reader, closer: z, MaxFileSize: DefaultMaxFileSize}, nil
}

// NewReader opens a Bambu 3MF file from an io.ReaderAt.
// This is useful for processing in-memory files or uploaded files.
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("failed to open 3mf reader: %w", err)
	}
	return &Reader{z: z, closer: nil, MaxFileSize: DefaultMaxFileSize}, nil
}

// Close closes the underlying zip file if opened via Open().
func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// ReadFile reads the named entry from the 3MF archive.
func (r *Reader) ReadFile(name string) ([]byte, error) {
	return r.readFileBytes(name)
}

// GetPackageThumbnail returns the archive-level thumbnail PNG (`Metadata/thumbnail.png`).
func (r *Reader) GetPackageThumbnail() ([]byte, error) {
	// Try standard or common paths
	// In the future this should use relationships
	return r.readFileBytes("Metadata/thumbnail.png")
}

func (r *Reader) readFileBytes(name string) ([]byte, error) {
	rc, err := r.openFile(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	maxSize := r.maxFileSize()
	lr := io.LimitReader(rc, maxSize+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}

	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file %s exceeds maximum size limit of %d bytes", name, maxSize)
	}

	return data, nil
}

func (r *Reader) maxFileSize() int64 {
	if r.MaxFileSize <= 0 {
		return DefaultMaxFileSize
	}
	return r.MaxFileSize
}

func (r *Reader) fileExists(name string) bool {
	for _, f := range r.z.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

func (r *Reader) openFile(name string) (io.ReadCloser, error) {
	for _, f := range r.z.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("file %s not found in 3mf", name)
}
