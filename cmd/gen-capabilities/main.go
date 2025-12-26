package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PrinterDefinition matches the structure of the JSON files in resources/printers
// These files are maps of "version" -> Definition.
// We are primarily interested in the "print" section of the definition.
type PrinterDefinition map[string]struct {
	DisplayName string `json:"display_name"`
	ModelID     string `json:"model_id"`
	Print       struct {
		NozzleTempRange    []int `json:"nozzle_temp_range"` // [min, max]
		BedTempLimit       int   `json:"bed_temp_limit"`    // Sometimes used instead of range
		SupportChamberFan  bool  `json:"support_chamber_fan"`
		SupportAuxFan      bool  `json:"support_aux_fan"`
		SupportAMSHumidity bool  `json:"support_ams_humidity"`
		SupportTimelapse   bool  `json:"support_timelapse"`
		SupportBedLeveling any   `json:"support_bed_leveling"` // Can be bool or int (1) in some files
	} `json:"print"`
}

// Capabilities is the simplified struct we export
type Capabilities struct {
	DisplayName    string `json:"display_name"`
	MaxNozzleTemp  int    `json:"max_nozzle_temp"`
	MaxBedTemp     int    `json:"max_bed_temp"`
	HasChamberFan  bool   `json:"has_chamber_fan"`
	HasAuxFan      bool   `json:"has_aux_fan"`
	HasAMSHumidity bool   `json:"has_ams_humidity"`
	HasTimelapse   bool   `json:"has_timelapse"`
	HasBedLeveling bool   `json:"has_bed_leveling"`
}

func main() {
	rootDir := "resources/printers"
	outputFile := "cmd/bambulan/printer_capabilities.json"

	capabilitiesMap := make(map[string]Capabilities)

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}

		// Skip blacklist or other non-printer files if any
		if strings.Contains(info.Name(), "blacklist") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		var def PrinterDefinition
		if err := json.Unmarshal(content, &def); err != nil {
			// Some files might not match exactly or be partials, log warn and continue
			fmt.Fprintf(os.Stderr, "Warning: could not unmarshal %s: %v\n", path, err)
			return nil
		}

		// We need to merge versions to get the "complete" picture.
		// Usually the base version (e.g. "00.00.00.00") has the static hardware flags.
		// Later versions might enable software features.
		// For now, we will aggregate "true" flags and take the max of temps.

		var caps Capabilities
		var modelID string

		// Iterate over all versions in the file
		for _, data := range def {
			// If we find a model ID, keep it. Usually in base version.
			if data.ModelID != "" {
				modelID = data.ModelID
			}
			if data.DisplayName != "" {
				caps.DisplayName = data.DisplayName
			}

			p := data.Print

			// Temps: take max found
			if len(p.NozzleTempRange) >= 2 {
				if p.NozzleTempRange[1] > caps.MaxNozzleTemp {
					caps.MaxNozzleTemp = p.NozzleTempRange[1]
				}
			}
			if p.BedTempLimit > caps.MaxBedTemp {
				caps.MaxBedTemp = p.BedTempLimit
			}

			// Boolean flags: if ANY version says true, we assume it's supported (hardware capability)
			if p.SupportChamberFan {
				caps.HasChamberFan = true
			}
			if p.SupportAuxFan {
				caps.HasAuxFan = true
			}
			if p.SupportAMSHumidity {
				caps.HasAMSHumidity = true
			}
			if p.SupportTimelapse {
				caps.HasTimelapse = true
			}

			// Bed Leveling: can be bool or int(1)
			switch v := p.SupportBedLeveling.(type) {
			case bool:
				if v {
					caps.HasBedLeveling = true
				}
			case float64:
				if v > 0 {
					caps.HasBedLeveling = true
				}
			}
		}

		if modelID != "" {
			capabilitiesMap[modelID] = caps
		}

		return nil
	})

	if err != nil {
		panic(err)
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(capabilitiesMap))
	for k := range capabilitiesMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Write output
	file, err := os.Create(outputFile)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(capabilitiesMap); err != nil {
		panic(err)
	}

	fmt.Printf("Successfully wrote %d printer capabilities to %s\n", len(capabilitiesMap), outputFile)
}
