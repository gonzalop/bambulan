package bambu3mf

import "encoding/xml"

// Metadata holds all extracted information from the 3MF file.
type Metadata struct {
	Plates    []Plate
	Filaments []Filament
}

// Plate represents a build plate in the 3MF project.
type Plate struct {
	ID             int
	Name           string
	ThumbnailPath  string // Path within the zip (e.g. Metadata/plate_1.png)
	ThumbnailSmall string // Path within the zip (e.g. Metadata/plate_1_small.png)
}

// Filament represents a filament used in the print.
type Filament struct {
	ID        int
	Type      string  // e.g. "PLA", "PETG"
	Color     string  // Hex color code (e.g. "#161616")
	UsedGrams float64 // Estimated usage in grams
}

// Internal structures for parsing

// slice_info.config
type sliceInfoConfig struct {
	XMLName xml.Name         `xml:"config"`
	Plates  []sliceInfoPlate `xml:"plate"`
}

type sliceInfoPlate struct {
	Metadata  []sliceInfoMetadata `xml:"metadata"`
	Filaments []sliceInfoFilament `xml:"filament"`
}

type sliceInfoMetadata struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

type sliceInfoFilament struct {
	ID        int     `xml:"id,attr"`
	Type      string  `xml:"type,attr"`
	Color     string  `xml:"color,attr"`
	UsedGrams float64 `xml:"used_g,attr"`
}

// plate_X.json
type plateInfo struct {
	PlateName string `json:"plate_name"` // content: optional
}
