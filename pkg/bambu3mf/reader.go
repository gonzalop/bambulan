package bambu3mf

import (
	"archive/zip"
	"fmt"
	"io"
)

// Reader provides access to the contents of a Bambu Lab 3MF file.
type Reader struct {
	z *zip.ReadCloser
}

// Open opens a Bambu 3MF file for reading.
// It returns a Reader that can be used to extract metadata and thumbnails.
// The caller must call Close() when done.
func Open(filename string) (*Reader, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open 3mf file: %w", err)
	}
	return &Reader{z: z}, nil
}

// Close closes the underlying zip file.
func (r *Reader) Close() error {
	return r.z.Close()
}

func (r *Reader) openFile(name string) (io.ReadCloser, error) {
	for _, f := range r.z.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("file %s not found in 3mf", name)
}
