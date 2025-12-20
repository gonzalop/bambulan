package filament

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadAll(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create Base Profile
	baseProfile := map[string]any{
		"type":        "filament",
		"name":        "Test Base",
		"filament_id": "BASE01",
		"nozzle_temperature": []any{
			"200",
		},
		"nozzle_temperature_range_high": []any{
			220,
		},
		"nozzle_temperature_range_low": []any{
			190,
		},
	}
	createJSON(t, tempDir, "base.json", baseProfile)

	// 2. Create Child Profile (inherits from Base)
	childProfile := map[string]any{
		"type":     "filament",
		"name":     "Test Child",
		"inherits": "Test Base",
		"hot_plate_temp": []any{
			"60",
		},
		"compatible_printers": []string{
			"Test Printer",
		},
	}
	createJSON(t, tempDir, "child.json", childProfile)

	// 3. Create Grandchild Profile (inherits from Child, overrides temo)
	grandchildProfile := map[string]any{
		"type":     "filament",
		"name":     "Test Grandchild",
		"inherits": "Test Child",
		"nozzle_temperature": []any{
			"210",
		},
	}
	createJSON(t, tempDir, "grandchild.json", grandchildProfile)

	// 4. Create Invalid Profile (wrong type)
	invalidProfile := map[string]any{
		"type": "process",
		"name": "Should Ignored",
	}
	createJSON(t, tempDir, "ignored.json", invalidProfile)

	// Run LoadAll
	filaments, err := LoadAll(tempDir)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	// Verify count (Base, Child, Grandchild)
	if len(filaments) != 3 {
		t.Errorf("Expected 3 filaments, got %d", len(filaments))
	}

	// Verify Inheritance and Parsing
	filMap := make(map[string]Filament)
	for _, f := range filaments {
		filMap[f.Name] = f
		// Debug print
		// t.Logf("Loaded: %+v", f)
	}

	// Check Base
	base := filMap["Test Base"]
	if base.TempMin != 190 || base.TempMax != 220 {
		t.Errorf("Base temp range mismatch: got %d-%d, want 190-220", base.TempMin, base.TempMax)
	}

	// Check Child
	child := filMap["Test Child"]
	if child.FilamentID != "BASE01" {
		t.Errorf("Child failed to inherit FilamentID: got %s", child.FilamentID)
	}
	if child.BedTemp != 60 {
		t.Errorf("Child failed to parse BedTemp: got %d", child.BedTemp)
	}
	if child.TempMin != 190 || child.TempMax != 220 {
		t.Errorf("Child failed to inherit temp range: got %d-%d", child.TempMin, child.TempMax)
	}

	// Check Grandchild
	grandchild := filMap["Test Grandchild"]
	if grandchild.FilamentID != "BASE01" {
		t.Errorf("Grandchild failed to inherit FilamentID: got %s", grandchild.FilamentID)
	}
	// It should use range from base, but target min from itself?
	// filament.go lines 82-87:
	// if fil.TempMin == 0 {
	//    if fil.rangeMin > 0 { fil.TempMin = fil.rangeMin } else { fil.TempMin = fil.targetMin }
	// }
	// Grandchild inherits rangeMin 190 from base. So TempMin should be 190.
	if grandchild.TempMin != 190 {
		t.Errorf("Grandchild TempMin mismatch: got %d", grandchild.TempMin)
	}
	// Check internal targetMin (which corresponds to nozzle_temperature)
	if grandchild.targetMin != 210 {
		t.Errorf("Grandchild targetMin mismatch: got %d, want 210", grandchild.targetMin)
	}
}

func TestFind(t *testing.T) {
	filaments := []Filament{
		{
			Name:       "Bambu PLA Basic",
			FilamentID: "GFA00",
			CompatiblePrinters: []string{
				"Bambu Lab X1 Carbon",
				"Bambu Lab P1P",
			},
		},
		{
			Name:       "Generic ABS",
			FilamentID: "GFB00",
			CompatiblePrinters: []string{
				"Bambu Lab X1 Carbon",
			},
		},
		{
			Name:       "Bambu PETG Basic",
			FilamentID: "GFC00", // Made up
			CompatiblePrinters: []string{
				"Bambu Lab A1",
			},
		},
	}

	tests := []struct {
		term   string
		model  string
		expect []string // Names
	}{
		{"PLA", "", []string{"Bambu PLA Basic"}},
		{"gfa00", "", []string{"Bambu PLA Basic"}},
		{"ABS", "X1 Carbon", []string{"Generic ABS"}},
		{"Basic", "X1 Carbon", []string{"Bambu PLA Basic"}}, // PETG incompatible with X1C in this mock data? No, valid test case implies filtering
		{"Basic", "", []string{"Bambu PLA Basic", "Bambu PETG Basic"}},
		{"XYZ", "", []string{}},
	}

	for _, tt := range tests {
		matches := Find(filaments, tt.term, tt.model)
		var names []string
		for _, m := range matches {
			names = append(names, m.Name)
		}
		if !reflect.DeepEqual(names, tt.expect) {
			// Handle empty slice vs nil slice
			if len(names) == 0 && len(tt.expect) == 0 {
				continue
			}
			t.Errorf("Find(%q, %q) = %v, want %v", tt.term, tt.model, names, tt.expect)
		}
	}
}

func TestLoadColors(t *testing.T) {
	tempDir := t.TempDir()
	content := `{
		"data": [
			{
				"fila_id": "GFA00",
				"fila_color": ["#FF0000"]
			},
			{
				"fila_id": "GFB00",
				"fila_color": ["#00FF00", "#0000FF"]
			}
		]
	}`
	path := filepath.Join(tempDir, "colors.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write colors file: %v", err)
	}

	colors, err := LoadColors(path)
	if err != nil {
		t.Fatalf("LoadColors failed: %v", err)
	}

	if colors["GFA00"] != "#FF0000" {
		t.Errorf("GFA00 color mismatch: got %s", colors["GFA00"])
	}
	if colors["GFB00"] != "#00FF00" { // Should pick first
		t.Errorf("GFB00 color mismatch: got %s", colors["GFB00"])
	}
}

func createJSON(t *testing.T, dir, name string, data any) {
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create file %s: %v", name, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(data); err != nil {
		t.Fatalf("Failed to encode JSON for %s: %v", name, err)
	}
}
