package filament

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Filament represents a filament profile parsed from a JSON file.
type Filament struct {
	Name               string   `json:"name"`
	FilamentID         string   `json:"filament_id"`
	SettingID          string   `json:"setting_id"`
	CompatiblePrinters []string `json:"compatible_printers"`
	NozzleTempMin      []any    `json:"nozzle_temperature"` // Can be int or string "nil" in JSON
	NozzleTempMax      []any    `json:"nozzle_temperature_maximum"`
	NozzleTempHigh     []any    `json:"nozzle_temperature_range_high"`
	NozzleTempLow      []any    `json:"nozzle_temperature_range_low"`
	HotPlateTemp       []any    `json:"hot_plate_temp"`
	TexturedPlateTemp  []any    `json:"textured_plate_temp"`
	CoolPlateTemp      []any    `json:"cool_plate_temp"`
	EngPlateTemp       []any    `json:"eng_plate_temp"`
	Type               string   `json:"type"` // Just "filament" usually
	Inherits           string   `json:"inherits"`

	// Synthesized fields
	TempMin int
	TempMax int
	BedTemp int // Representative bed temp (e.g. from hot/textured plate)

	// Internal resolution fields
	rangeMin  int
	rangeMax  int
	targetMin int
}

// LoadAll reads all JSON files in the given directory and returns a list of Filaments.
// It skips files that don't look like filament profiles (missing name/type).
func LoadAll(rootDir string) ([]Filament, error) {
	// Phase 1: Load all into a map by Name
	filMap := make(map[string]*Filament)

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil // Skip unreadable
		}
		defer f.Close()

		var fil Filament
		if err := json.NewDecoder(f).Decode(&fil); err != nil {
			return nil // Skip invalid JSON
		}

		if fil.Type != "filament" || fil.Name == "" {
			return nil
		}

		filMap[fil.Name] = &fil
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Phase 2: Resolve inheritance and parse values
	var filaments []Filament
	for _, fil := range filMap {
		resolve(fil, filMap, 0)

		// Finalize: prefer range over target
		if fil.TempMin == 0 {
			if fil.rangeMin > 0 {
				fil.TempMin = fil.rangeMin
			} else {
				fil.TempMin = fil.targetMin
			}
		}
		if fil.TempMax == 0 {
			if fil.rangeMax > 0 {
				fil.TempMax = fil.rangeMax
			}
			// If no range max, we could optionally use targetMin as a fallback bound,
			// but better to leave 0 and let CLI handle defaults or user input.
		}

		filaments = append(filaments, *fil)
	}

	return filaments, nil
}

// resolve populates missing fields from parents (up to 10 levels deep)
func resolve(f *Filament, all map[string]*Filament, depth int) {
	if depth > 10 {
		return
	}
	// Parse own values from JSON fields into internal int fields
	parseValues(f)

	// If we have fully resolved everything important, we could stop,
	// but with split range/target we should ensure we get the best data.
	// We'll check synthesized BedTemp and IDs.
	if f.FilamentID != "" && f.BedTemp > 0 && f.rangeMin > 0 && f.rangeMax > 0 {
		return
	}

	if f.Inherits != "" {
		parent, ok := all[f.Inherits]
		if ok {
			resolve(parent, all, depth+1)

			// Inherit properties if missing
			if f.FilamentID == "" {
				f.FilamentID = parent.FilamentID
			}
			if f.BedTemp == 0 {
				f.BedTemp = parent.BedTemp
			}

			// Inherit resolution fields
			if f.rangeMin == 0 {
				f.rangeMin = parent.rangeMin
			}
			if f.rangeMax == 0 {
				f.rangeMax = parent.rangeMax
			}
			if f.targetMin == 0 {
				f.targetMin = parent.targetMin
			}
		}
	}
}

func parseValues(f *Filament) {
	// Parse Range Low
	if f.rangeMin == 0 {
		f.rangeMin = parseFirstInt(f.NozzleTempLow)
	}

	// Parse Range Max
	if f.rangeMax == 0 {
		f.rangeMax = parseFirstInt(f.NozzleTempHigh)
		if f.rangeMax == 0 {
			f.rangeMax = parseFirstInt(f.NozzleTempMax)
		}
	}

	// Parse Target
	if f.targetMin == 0 {
		f.targetMin = parseFirstInt(f.NozzleTempMin)
	}

	if f.BedTemp == 0 {
		f.BedTemp = parseFirstInt(f.HotPlateTemp)
		if f.BedTemp == 0 {
			f.BedTemp = parseFirstInt(f.TexturedPlateTemp)
		}
		if f.BedTemp == 0 {
			f.BedTemp = parseFirstInt(f.CoolPlateTemp)
		}
		if f.BedTemp == 0 {
			f.BedTemp = parseFirstInt(f.EngPlateTemp)
		}
	}
}

func parseFirstInt(val []any) int {
	if len(val) == 0 {
		return 0
	}
	v := val[0]
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		var i int
		if _, err := fmt.Sscanf(t, "%d", &i); err == nil {
			return i
		}
	}
	return 0
}

// Find searches for filaments matching the term (case-insensitive substring of Name or ID).
// printerModel is optional; if provided, filters matches to those compatible.
func Find(filaments []Filament, term string, printerModel string) []Filament {
	term = strings.ToLower(term)
	var matches []Filament

	for _, f := range filaments {
		// 1. Basic match: Name or ID
		match := strings.Contains(strings.ToLower(f.Name), term) ||
			strings.EqualFold(f.FilamentID, term) ||
			strings.EqualFold(f.SettingID, term)

		if !match {
			continue
		}

		// 2. Filter by printer model if needed
		// Note: Most "leaf" profiles have compatible_printers.
		// "base" profiles might not, or they might be generic.
		// If we find a specific profile (`setting_id` present), it usually has compatible_printers.
		if printerModel != "" && len(f.CompatiblePrinters) > 0 {
			isCompatible := false
			for _, p := range f.CompatiblePrinters {
				if strings.Contains(strings.ToLower(p), strings.ToLower(printerModel)) {
					isCompatible = true
					break
				}
			}
			if !isCompatible {
				continue
			}
		}

		matches = append(matches, f)
	}
	return matches
}

// ColorDef represents an entry in filaments_color_codes.json
type ColorDef struct {
	FilaID    string   `json:"fila_id"`
	FilaColor []string `json:"fila_color"`
}

type ColorFile struct {
	Data []ColorDef `json:"data"`
}

// LoadColors loads the default color for each filament ID from the given JSON file.
// Returns a map of FilamentID -> HexColor (e.g. "GFA16" -> "918669FF").
// Since IDs can have multiple colors, this returns the first one found.
func LoadColors(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cf ColorFile
	if err := json.NewDecoder(f).Decode(&cf); err != nil {
		return nil, err
	}

	colors := make(map[string]string)
	for _, d := range cf.Data {
		if len(d.FilaColor) > 0 && d.FilaID != "" {
			// Only set if not already set (keep first occurrence)
			if _, exists := colors[d.FilaID]; !exists {
				colors[d.FilaID] = d.FilaColor[0]
			}
		}
	}
	return colors, nil
}
