package bambu3mf

import (
	"archive/zip"
	"fmt"
	"io"
)

// Reader provides access to the contents of a Bambu Lab 3MF file.
// Reader provides access to the contents of a Bambu Lab 3MF file.
type Reader struct {
	z      *zip.Reader
	closer io.Closer
}

// Open opens a Bambu 3MF file for reading.
// It returns a Reader that can be used to extract metadata and thumbnails.
// The caller must call Close() when done.
func Open(filename string) (*Reader, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open 3mf file: %w", err)
	}
	return &Reader{z: &z.Reader, closer: z}, nil
}

// NewReader opens a Bambu 3MF file from an io.ReaderAt.
// This is useful for processing in-memory files or uploaded files.
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("failed to open 3mf reader: %w", err)
	}
	return &Reader{z: z, closer: nil}, nil
}

// Close closes the underlying zip file if opened via Open().
func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

func (r *Reader) ReadFile(name string) ([]byte, error) {
	return r.readFileBytes(name)
}

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
	return io.ReadAll(rc)
}

func (r *Reader) openFile(name string) (io.ReadCloser, error) {
	for _, f := range r.z.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("file %s not found in 3mf", name)
}
