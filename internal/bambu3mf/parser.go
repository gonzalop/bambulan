package bambu3mf

import (
	"encoding/json/v2"
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

	// 0. Parse Standard Metadata
	_ = r.parseStandardMetadata(md)

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

		// Default thumbnail path
		plate.ThumbnailPath = fmt.Sprintf("Metadata/plate_%d.png", plateID)
		plate.ThumbnailSmall = fmt.Sprintf("Metadata/plate_%d_small.png", plateID)

		// Check for override in metadata
		for _, m := range p.Metadata {
			if m.Key == "thumbnail_file" && m.Value != "" {
				plate.ThumbnailPath = m.Value
				// Guess small thumbnail if not explicitly provided (rarely is)
				// usually convention is name_small.png or similar, but let's stick to default or try to derive?
				// For now keep default small path or assume it matches extension of main?
			}
			if m.Key == "thumbnail_small_file" && m.Value != "" {
				plate.ThumbnailSmall = m.Value
			}
		}

		// A printer prints one plate per job, and callers show the first plate,
		// so report that plate's filaments rather than a sum over all plates.
		if len(md.Plates) == 0 {
			for _, f := range p.Filaments {
				md.Filaments = append(md.Filaments, Filament(f))
			}
		}

		// Find plate name from json
		jsonFile := fmt.Sprintf("Metadata/plate_%d.json", plateID)
		if name, err := r.parsePlateName(jsonFile); err == nil && name != "" {
			plate.Name = name
		} else {
			plate.Name = fmt.Sprintf("Plate %d", plateID)
		}

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
	lr := io.LimitReader(rc, r.maxFileSize()+1)
	if err := xml.NewDecoder(lr).Decode(&config); err != nil {
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
	lr := io.LimitReader(rc, r.maxFileSize()+1)
	if err := json.UnmarshalRead(lr, &pi); err != nil {
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
func (r *Reader) parseStandardMetadata(md *Metadata) error {
	// Standard location for 3D Model part is usually 3D/3dmodel.model
	// But technically it should be found via _rels/.rels.
	// For simplicity and speed, we check the default path first.
	// If needed later we can implement full OPC relationships.

	if rc, err := r.openFile("3D/3dmodel.model"); err == nil {
		defer rc.Close()

		// XML structure for <model> with <metadata> children
		type metadataXML struct {
			Name    string `xml:"name,attr"`
			Content string `xml:",chardata"`
		}

		type modelXML struct {
			XMLName  xml.Name      `xml:"model"`
			Metadata []metadataXML `xml:"metadata"`
		}

		var m modelXML
		lr := io.LimitReader(rc, r.maxFileSize()+1)
		if err := xml.NewDecoder(lr).Decode(&m); err == nil {
			for _, meta := range m.Metadata {
				switch meta.Name {
				case "Title":
					md.Title = meta.Content
				case "Designer":
					md.Designer = meta.Content
				case "Description":
					md.Description = meta.Content
				case "Copyright":
					md.Copyright = meta.Content
				case "LicenseTerms":
					md.LicenseTerms = meta.Content
				case "CreationDate":
					md.CreationDate = meta.Content
				case "ModificationDate":
					md.ModificationDate = meta.Content
				case "Application":
					md.Application = meta.Content
				}
			}
		}
	}

	// Check for standard package thumbnail without opening reader
	if r.fileExists("Metadata/thumbnail.png") {
		md.ThumbnailPath = "Metadata/thumbnail.png"
	}

	return nil
}
