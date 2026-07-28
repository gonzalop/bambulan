package bambulan

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
)

//go:embed printer_capabilities.json
var printerCapabilitiesJSON []byte

// printerCapabilitiesMap is the cached map of capabilities
var (
	printerCapabilitiesMap map[string]PrinterCapability
	aliasCapabilitiesMap   map[string]PrinterCapability
	capabilitiesOnce       sync.Once
)

// PrinterCapability defines the supported features and limitations for a specific printer model.
type PrinterCapability struct {
	DisplayName             string `json:"display_name"`
	MaxNozzleTemp           int    `json:"max_nozzle_temp"`
	MaxBedTemp              int    `json:"max_bed_temp"`
	HasChamberFan           bool   `json:"has_chamber_fan"`
	HasAuxFan               bool   `json:"has_aux_fan"`
	HasAMSHumidity          bool   `json:"has_ams_humidity"`
	HasAMSCapacityReporting bool   `json:"has_ams_capacity_reporting"`
	HasTimelapse            bool   `json:"has_timelapse"`
	HasBedLeveling          bool   `json:"has_bed_leveling"`
	HasChamberHeater        bool   `json:"has_chamber_heater"`
	HasChamberTemp          bool   `json:"has_chamber_temp"`
	MinChamberTemp          int    `json:"min_chamber_temp"`
	MaxChamberTemp          int    `json:"max_chamber_temp"`
	NumExtruders            int    `json:"num_extruders"`
}

var defaultPrinterCapability = PrinterCapability{
	DisplayName:             "Bambu Lab Printer",
	MaxNozzleTemp:           300,
	MaxBedTemp:              100,
	HasChamberFan:           true,
	HasAuxFan:               true,
	HasAMSHumidity:          true,
	HasAMSCapacityReporting: true,
	HasTimelapse:            true,
	HasBedLeveling:          true,
}

// InferModelFromSerial returns the model ID (e.g. "C12", "BL-P001") based on the 3-character serial number prefix.
func InferModelFromSerial(serial string) string {
	if len(serial) < 3 {
		return ""
	}
	prefix := strings.ToUpper(serial[:3])
	switch prefix {
	case "00M", "00W":
		return "BL-P001" // X1C
	case "001":
		return "BL-P002" // X1
	case "01S", "01C":
		return "C12" // P1S
	case "01P":
		return "C11" // P1P
	case "030", "03N":
		return "N1" // A1 Mini
	case "01N":
		return "N2S" // A1
	case "039":
		return "C13" // X1E
	default:
		return ""
	}
}

func normalizeModelKey(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "bambu lab ")
	s = strings.TrimPrefix(s, "bambu ")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

// GetPrinterCapabilities returns the capabilities for the given printer model ID, model name, or serial number.
//
// If the model ID is unknown or empty, this function returns a default PrinterCapability struct with
// common enclosed printer capabilities (enabling fan and temp controls).
func GetPrinterCapabilities(modelID string) PrinterCapability {
	ensureCapabilitiesLoaded()
	if modelID == "" {
		return defaultPrinterCapability
	}

	// 1. Direct lookup
	if capabilities, ok := printerCapabilitiesMap[modelID]; ok {
		return capabilities
	}

	// 2. Normalized alias lookup
	normKey := normalizeModelKey(modelID)
	if capabilities, ok := aliasCapabilitiesMap[normKey]; ok {
		return capabilities
	}

	// 3. Serial prefix lookup
	if inferredModel := InferModelFromSerial(modelID); inferredModel != "" {
		if capabilities, ok := printerCapabilitiesMap[inferredModel]; ok {
			return capabilities
		}
	}

	// 4. Default fallback for unknown models
	return defaultPrinterCapability
}

// ensureCapabilitiesLoaded lazily unmarshals the embedded JSON data into the global map.
// This operation is thread-safe and performed only once.
func ensureCapabilitiesLoaded() {
	capabilitiesOnce.Do(func() {
		printerCapabilitiesMap = make(map[string]PrinterCapability)
		aliasCapabilitiesMap = make(map[string]PrinterCapability)

		if err := json.Unmarshal(printerCapabilitiesJSON, &printerCapabilitiesMap); err != nil {
			slog.Error("Failed to unmarshal printer capabilities", "error", err)
			return
		}

		// Populate alias map
		for code, cap := range printerCapabilitiesMap {
			aliasCapabilitiesMap[normalizeModelKey(code)] = cap
			aliasCapabilitiesMap[normalizeModelKey(cap.DisplayName)] = cap
		}

		// Additional common aliases
		if c12, ok := printerCapabilitiesMap["C12"]; ok {
			aliasCapabilitiesMap["p1s"] = c12
		}
		if c11, ok := printerCapabilitiesMap["C11"]; ok {
			aliasCapabilitiesMap["p1p"] = c11
		}
		if blp001, ok := printerCapabilitiesMap["BL-P001"]; ok {
			aliasCapabilitiesMap["x1c"] = blp001
			aliasCapabilitiesMap["x1carbon"] = blp001
		}
		if blp002, ok := printerCapabilitiesMap["BL-P002"]; ok {
			aliasCapabilitiesMap["x1"] = blp002
		}
		if c13, ok := printerCapabilitiesMap["C13"]; ok {
			aliasCapabilitiesMap["x1e"] = c13
		}
		if n1, ok := printerCapabilitiesMap["N1"]; ok {
			aliasCapabilitiesMap["a1mini"] = n1
		}
		if n2s, ok := printerCapabilitiesMap["N2S"]; ok {
			aliasCapabilitiesMap["a1"] = n2s
		}
	})
}
