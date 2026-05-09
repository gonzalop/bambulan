package bambulan

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

// PrinterCapability defines the supported features and limitations for a specific printer model.
// This structure is populated from the embedded printer_capabilities.json file, which is generated
// from the official Bambu Lab printer definitions.
type PrinterCapability struct {
	// DisplayName is the human-readable name of the printer (e.g., "Bambu Lab X1 Carbon").
	DisplayName string `json:"display_name"`

	// MaxNozzleTemp is the maximum safe temperature for the nozzle in degrees Celsius.
	MaxNozzleTemp int `json:"max_nozzle_temp"`

	// MaxBedTemp is the maximum safe temperature for the heatbed in degrees Celsius.
	MaxBedTemp int `json:"max_bed_temp"`

	// HasChamberFan indicates whether the printer model is equipped with a chamber ventilation fan.
	HasChamberFan bool `json:"has_chamber_fan"`

	// HasAuxFan indicates whether the printer model supports an auxiliary part cooling fan.
	HasAuxFan bool `json:"has_aux_fan"`

	// HasAMSHumidity indicates whether the printer supports reporting AMS humidity levels.
	HasAMSHumidity bool `json:"has_ams_humidity"`

	// HasAMSCapacityReporting indicates whether the printer supports reporting AMS filament capacity/remaining.
	HasAMSCapacityReporting bool `json:"has_ams_capacity_reporting"`

	// HasTimelapse indicates whether the printer supports internal timelapse recording.
	HasTimelapse bool `json:"has_timelapse"`

	// HasBedLeveling indicates whether the printer supports automatic bed leveling.
	HasBedLeveling bool `json:"has_bed_leveling"`

	// HasChamberHeater indicates whether the printer has an active chamber heater.
	HasChamberHeater bool `json:"has_chamber_heater"`

	// MaxChamberTemp is the maximum target temperature for the chamber heater.
	MaxChamberTemp int `json:"max_chamber_temp"`

	// NumExtruders is the number of tool heads / extruders available on the printer.
	NumExtruders int `json:"num_extruders"`
}

// GetPrinterCapabilities returns the capabilities for the given printer model ID (e.g., "BL-P001", "C11").
// The model ID is typically reported by the printer in its MQTT status messages.
//
// If the model ID is unknown or not found in the embedded database, this function returns an empty
// PrinterCapability struct. Callers should check if DisplayName is empty to determine if a valid
// capability set was returned.
func GetPrinterCapabilities(modelID string) PrinterCapability {
	ensureCapabilitiesLoaded()
	if capabilities, ok := printerCapabilitiesMap[modelID]; ok {
		return capabilities
	}
	return PrinterCapability{}
}

// ensureCapabilitiesLoaded lazily unmarshals the embedded JSON data into the global map.
// This operation is thread-safe and performed only once.
func ensureCapabilitiesLoaded() {
	capabilitiesOnce.Do(func() {
		printerCapabilitiesMap = make(map[string]PrinterCapability)
		if err := json.Unmarshal(printerCapabilitiesJSON, &printerCapabilitiesMap); err != nil {
			slog.Error("Failed to unmarshal printer capabilities", "error", err)
		}
	})
}
