package bambulan

import (
	"fmt"
	"testing"
)

func TestCapabilities(t *testing.T) {
	// Test X1C
	x1c := GetPrinterCapabilities("BL-P001")
	if x1c.DisplayName != "Bambu Lab X1 Carbon" {
		t.Errorf("Expected X1 Carbon, got %s", x1c.DisplayName)
	}
	if !x1c.HasChamberFan {
		t.Error("X1C should have chamber fan")
	}
	if x1c.MaxNozzleTemp != 300 {
		t.Errorf("expected max nozzle temp 300, got %d", x1c.MaxNozzleTemp)
	}

	if !x1c.HasAMSCapacityReporting {
		t.Error("expected X1C to support AMS capacity reporting")
	}

	// Test A2L
	a2l := GetPrinterCapabilities("N9")
	if a2l.DisplayName != "Bambu Lab A2L" {
		t.Errorf("Expected A2L, got %s", a2l.DisplayName)
	}
	if a2l.MaxNozzleTemp != 300 {
		t.Errorf("expected max nozzle temp 300, got %d", a2l.MaxNozzleTemp)
	}

	fmt.Println("Capabilities test passed")
}
