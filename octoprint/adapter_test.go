package octoprint

import (
	"context"
	"testing"

	"github.com/gonzalop/bambulan"
)

func TestAdapter_Version(t *testing.T) {
	a := NewAdapter(nil)
	v := a.Version()
	if v.API == "" || v.Server == "" {
		t.Errorf("Version() returned empty fields: %+v", v)
	}
}

func TestAdapter_PrinterProfiles(t *testing.T) {
	a := NewAdapter(nil)

	tests := []struct {
		model  string
		width  int
		depth  int
		height int
	}{
		{"BL-P001", 256, 256, 256}, // X1C
		{"N1", 180, 180, 180},      // A1 mini
		{"C11", 256, 256, 256},     // P1P
		{"unknown", 256, 256, 256}, // Default
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			st := &bambulan.PrinterStatus{DeviceModel: tt.model}
			resp := a.PrinterProfiles(st)
			profile, ok := resp.Profiles["_default"]
			if !ok {
				t.Fatal("Profile '_default' not found")
			}
			if profile.Volume.Width != tt.width || profile.Volume.Depth != tt.depth || profile.Volume.Height != tt.height {
				t.Errorf("Model %s: expected %dx%dx%d, got %dx%dx%d",
					tt.model, tt.width, tt.depth, tt.height,
					profile.Volume.Width, profile.Volume.Depth, profile.Volume.Height)
			}
		})
	}
}

func TestAdapter_PrinterState(t *testing.T) {
	a := NewAdapter(nil)
	ctx := context.Background()

	st := &bambulan.PrinterStatus{
		NozzleTemp:       210.5,
		NozzleTargetTemp: 220.0,
		BedTemp:          60.0,
		BedTargetTemp:    60.0,
		GcodeState:       "RUNNING",
		DeviceModel:      "BL-P001",
	}

	resp, err := a.PrinterState(ctx, st)
	if err != nil {
		t.Fatalf("PrinterState() failed: %v", err)
	}

	if resp.Temperature["tool0"].Actual != 210.5 || resp.Temperature["tool0"].Target != 220.0 {
		t.Errorf("Incorrect nozzle temps: %+v", resp.Temperature["tool0"])
	}
	if resp.Temperature["bed"].Actual != 60.0 || resp.Temperature["bed"].Target != 60.0 {
		t.Errorf("Incorrect bed temps: %+v", resp.Temperature["bed"])
	}
	if !resp.State.Flags.Printing {
		t.Error("Expected Printing flag to be true")
	}
	if resp.State.Flags.Ready {
		t.Error("Expected Ready flag to be false while printing")
	}
}
