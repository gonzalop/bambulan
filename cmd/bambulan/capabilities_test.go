package main

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

	// Test A1 Mini
	a1m := GetPrinterCapabilities("N1")
	if a1m.DisplayName != "Bambu Lab A1 mini" {
		t.Errorf("Expected A1 mini, got %s", a1m.DisplayName)
	}
	if a1m.HasChamberFan {
		t.Error("A1 mini should NOT have chamber fan")
	}

	fmt.Println("Capabilities test passed")
}
