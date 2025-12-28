package bambulan

import (
	"testing"
)

func TestTempLimitsRefactor(t *testing.T) {
	// Test cases based on printer_capabilities.json
	tests := []struct {
		model          string
		expectedBed    int
		expectedNozzle int
	}{
		{"BL-P001", 110, 300}, // X1C
		{"N1", 80, 300},       // A1 Mini
		{"C11", 100, 300},     // P1P
		{"C13", 110, 320},     // X1E
		{"Unknown", 100, 300}, // Fallback
	}

	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			gotBed := getBedTempLimit(tc.model)
			if gotBed != tc.expectedBed {
				t.Errorf("getBedTempLimit(%s) = %d; want %d", tc.model, gotBed, tc.expectedBed)
			}

			gotNozzle := getNozzleTempLimit(tc.model)
			if gotNozzle != tc.expectedNozzle {
				t.Errorf("getNozzleTempLimit(%s) = %d; want %d", tc.model, gotNozzle, tc.expectedNozzle)
			}
		})
	}
}
