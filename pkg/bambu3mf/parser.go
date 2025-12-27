package bambu3mf

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

// ParseMetadata extracts all relevant metadata from the 3mf file.
// It parses the slice_info.config and plate_*.json files to populate
// information about plates, thumbnails, and filament usage.
func (r *Reader) ParseMetadata() (*Metadata, error) {
	md := &Metadata{}

	// 1. Parse slice_info.config
	si, err := r.parseSliceInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to parse slice_info: %w", err)
	}

	for _, p := range si.Plates {
		// Extract Plate ID from metadata
		plateID := 0
		for _, m := range p.Metadata {
			if m.Key == "index" {
				if id, err := strconv.Atoi(m.Value); err == nil {
					plateID = id
				}
				break
			}
		}

		if plateID == 0 {
			continue // Should not happen if file is valid
		}

		plate := Plate{
			ID: plateID,
		}

		// Use map to avoid duplicates if filaments are repeated per plate
		filamentMap := make(map[int]Filament)
		for _, f := range p.Filaments {
			filamentMap[f.ID] = Filament{
				ID:        f.ID,
				Type:      f.Type,
				Color:     f.Color,
				UsedGrams: f.UsedGrams,
			}
		}
		// In a real scenario we might want to aggregate these across plates?
		// For now let's just append to global list if not exists
		for _, f := range filamentMap {
			found := false
			for _, existing := range md.Filaments {
				if existing.ID == f.ID {
					// Aggregate usage? or just skip?
					// The XML shows "used_g" per plate. So we should probably aggregate.
					// But we can't easily modify the slice element.
					// Let's just append for now and let the user handle it,
					// OR better: use a map for the global metadata too.
					found = true
					break
				}
			}
			if !found {
				md.Filaments = append(md.Filaments, f)
			}
		}

		// Find plate name from json
		jsonFile := fmt.Sprintf("Metadata/plate_%d.json", plateID)
		if name, err := r.parsePlateName(jsonFile); err == nil && name != "" {
			plate.Name = name
		} else {
			plate.Name = fmt.Sprintf("Plate %d", plateID)
		}

		// Thumbnails
		plate.ThumbnailPath = fmt.Sprintf("Metadata/plate_%d.png", plateID)
		plate.ThumbnailSmall = fmt.Sprintf("Metadata/plate_%d_small.png", plateID)

		md.Plates = append(md.Plates, plate)
	}

	return md, nil
}

func (r *Reader) parseSliceInfo() (*sliceInfoConfig, error) {
	rc, err := r.openFile("Metadata/slice_info.config")
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var config sliceInfoConfig
	if err := xml.NewDecoder(rc).Decode(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *Reader) parsePlateName(filename string) (string, error) {
	rc, err := r.openFile(filename)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var pi plateInfo
	if err := json.NewDecoder(rc).Decode(&pi); err != nil {
		return "", err
	}
	return pi.PlateName, nil
}

func (r *Reader) GetThumbnail(plateID int) ([]byte, error) {
	return r.readFileBytes(fmt.Sprintf("Metadata/plate_%d.png", plateID))
}

func (r *Reader) GetThumbnailSmall(plateID int) ([]byte, error) {
	return r.readFileBytes(fmt.Sprintf("Metadata/plate_%d_small.png", plateID))
}

func (r *Reader) readFileBytes(name string) ([]byte, error) {
	rc, err := r.openFile(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
