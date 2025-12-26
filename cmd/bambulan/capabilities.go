package main

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"sync"
)

//go:embed printer_capabilities.json
var printerCapabilitiesJSON []byte

// printerCapabilitiesMap is the cached map of capabilities
var (
	printerCapabilitiesMap map[string]PrinterCapability
	capabilitiesOnce       sync.Once
)

// PrinterCapability defines the supported features for a specific printer model.
type PrinterCapability struct {
	DisplayName    string `json:"display_name"`
	MaxNozzleTemp  int    `json:"max_nozzle_temp"`
	MaxBedTemp     int    `json:"max_bed_temp"`
	HasChamberFan  bool   `json:"has_chamber_fan"`
	HasAuxFan      bool   `json:"has_aux_fan"`
	HasAMSHumidity bool   `json:"has_ams_humidity"`
	HasTimelapse   bool   `json:"has_timelapse"`
	HasBedLeveling bool   `json:"has_bed_leveling"`
}

// GetPrinterCapabilities returns the capabilities for the given model ID (e.g., "BL-P001").
// If the model is not found, it returns a default capability set with basic features enabled,
// to be safe, or zero values if preferred.
// For now, we return zero values if unknown, caller can check DisplayName != "".
func GetPrinterCapabilities(modelID string) PrinterCapability {
	ensureCapabilitiesLoaded()
	if cap, ok := printerCapabilitiesMap[modelID]; ok {
		return cap
	}
	// Return default or empty?
	// Let's return a "generic" safe default, or just empty.
	// Users might have unknown newer printers.
	// For now, returning empty struct.
	return PrinterCapability{}
}

func ensureCapabilitiesLoaded() {
	capabilitiesOnce.Do(func() {
		printerCapabilitiesMap = make(map[string]PrinterCapability)
		if err := json.Unmarshal(printerCapabilitiesJSON, &printerCapabilitiesMap); err != nil {
			slog.Error("Failed to unmarshal printer capabilities", "error", err)
		}
	})
}
