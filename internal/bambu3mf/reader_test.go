package bambu3mf

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestDecompressionBomb(t *testing.T) {
	// Create an in-memory zip file with a large uncompressed entry
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Create a file entry that is larger than the 50MB limit
	// A file with 51MB of zeroes is highly compressible
	w, err := zw.Create("bomb.txt")
	if err != nil {
		t.Fatalf("Failed to create zip entry: %v", err)
	}
	largeData := make([]byte, 51*1024*1024)
	if _, err := w.Write(largeData); err != nil {
		t.Fatalf("Failed to write large data: %v", err)
	}
	zw.Close()

	// Parse it
	r, err := NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer r.Close()

	// Attempt to read the large file
	_, err = r.ReadFile("bomb.txt")
	if err == nil {
		t.Error("Expected error for large file, but got nil")
	} else if !strings.Contains(err.Error(), "exceeds maximum size limit") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParseMetadata(t *testing.T) {
	// Adjust path as needed for where tests are running
	// If running from module root: resources/...
	filename := "../../resources/3mf-decompression/hinge/hinge_right.gcode.3mf"
	r, err := Open(filename)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	md, err := r.ParseMetadata()

	if err != nil {
		t.Fatalf("ParseMetadata failed: %v", err)
	}

	t.Logf("Parsed Metadata: %+v", md)

	if len(md.Plates) == 0 {
		t.Error("Expected at least one plate")
	}

	for _, p := range md.Plates {
		t.Logf("Plate: %d %s", p.ID, p.Name)
		if p.Name == "" {
			t.Error("Plate name is empty")
		}

		// Check thumbnails
		b, err := r.GetThumbnail(p.ID)
		if err != nil {
			t.Errorf("Failed to get thumbnail for plate %d: %v", p.ID, err)
		}
		if len(b) == 0 {
			t.Errorf("Thumbnail empty for plate %d", p.ID)
		}

		bSmall, err := r.GetThumbnailSmall(p.ID)
		if err != nil {
			t.Errorf("Failed to get small thumbnail for plate %d: %v", p.ID, err)
		}
		if len(bSmall) == 0 {
			t.Errorf("Small thumbnail empty for plate %d", p.ID)
		}
	}

	if len(md.Filaments) == 0 {
		t.Error("Expected at least one filament")
	}

	for _, f := range md.Filaments {
		t.Logf("Filament: %d %s %s %.2fg", f.ID, f.Type, f.Color, f.UsedGrams)
		if f.UsedGrams <= 0 {
			t.Logf("Warning: Filament %d has 0 usage?", f.ID)
		}
	}

	// Standard Metadata checks (may be empty for this specific test file, but should not panic)
	t.Logf("Standard Metadata: Title=%q Designer=%q Description=%q", md.Title, md.Designer, md.Description)
	if md.ThumbnailPath != "" {
		t.Logf("Found Package Thumbnail at %s", md.ThumbnailPath)
		// Try to read it
		if _, err := r.GetPackageThumbnail(); err != nil {
			t.Errorf("Failed to read package thumbnail: %v", err)
		}
	}
}
