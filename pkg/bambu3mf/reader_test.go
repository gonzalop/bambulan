package bambu3mf

import (
	"testing"
)

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
}
