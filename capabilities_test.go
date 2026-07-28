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

	// Test Aliases
	p1s := GetPrinterCapabilities("P1S")
	if p1s.DisplayName != "Bambu Lab P1S" || !p1s.HasAuxFan || !p1s.HasChamberFan {
		t.Errorf("Expected P1S with fans, got %+v", p1s)
	}

	p1sLower := GetPrinterCapabilities("p1s")
	if p1sLower.DisplayName != "Bambu Lab P1S" {
		t.Errorf("Expected P1S via lowercase alias, got %s", p1sLower.DisplayName)
	}

	// Test Serial Lookup
	p1sSerial := GetPrinterCapabilities("01S00A12345678")
	if p1sSerial.DisplayName != "Bambu Lab P1S" {
		t.Errorf("Expected P1S via serial prefix, got %s", p1sSerial.DisplayName)
	}

	// Test Fallback
	fallback := GetPrinterCapabilities("Printer")
	if !fallback.HasAuxFan || !fallback.HasChamberFan {
		t.Errorf("Expected default fallback with fans enabled, got %+v", fallback)
	}

	fmt.Println("Capabilities test passed")
}
