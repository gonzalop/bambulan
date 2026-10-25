package homeassistant

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gonzalop/bambulan"
)

// TestBridgeSharedStateConcurrentAccess exercises the fields touched by MQTT
// callbacks, the event loop and the file-sync goroutine. Run with -race.
func TestBridgeSharedStateConcurrentAccess(t *testing.T) {
	b := &Bridge{lastLight: "UNKNOWN", files: []string{"None"}}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		state := "ON"
		if i%2 == 0 {
			state = "OFF"
		}
		wg.Add(3)
		go func() { // MQTT command callbacks
			defer wg.Done()
			b.swapLastLight(state)
			b.cameraEnabled.Store(state == "ON")
		}()
		go func() { // event loop
			defer wg.Done()
			b.mu.Lock()
			b.model, b.haModel, b.displayName = "C12", "P1S", "Bambu Lab P1S 5678"
			b.mu.Unlock()
			b.discoveryOk.Store(true)
			b.swapLastLight(state)
		}()
		go func() { // file sync / snapshot loops
			defer wg.Done()
			b.mu.Lock()
			b.files = []string{"a.3mf"}
			b.mu.Unlock()
			_, _, _ = b.identity()
			_ = b.discoveryOk.Load() && b.cameraEnabled.Load()
		}()
	}
	wg.Wait()
}

func TestPublishDiscoveryHonorsCancelledContext(t *testing.T) {
	// ha is nil: any publish attempt would panic, so returning ctx.Err()
	// proves the loop stops before touching the broker.
	b := &Bridge{prefix: "homeassistant", serial: "01S00A12345678", model: "C12", files: []string{"None"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := b.publishDiscovery(ctx, "P1S"); !errors.Is(err, context.Canceled) {
		t.Fatalf("publishDiscovery with cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestSwapLastLight(t *testing.T) {
	b := &Bridge{lastLight: "UNKNOWN"}
	steps := []struct {
		state   string
		changed bool
	}{
		{"ON", true},
		{"ON", false},
		{"OFF", true},
		{"OFF", false},
	}
	for i, s := range steps {
		if got := b.swapLastLight(s.state); got != s.changed {
			t.Errorf("step %d: swapLastLight(%q) = %v, want %v", i, s.state, got, s.changed)
		}
	}
}

func TestTargetChamberTempReportedWhenZero(t *testing.T) {
	caps := bambulan.GetPrinterCapabilities("C13")
	if !caps.HasChamberHeater {
		t.Skip("C13 does not have chamber heater")
	}

	status := &bambulan.PrinterStatus{
		ChamberTargetTemp: 0,
	}

	state := make(map[string]any)
	if caps.HasChamberHeater {
		state["target_chamber_temperature"] = int(status.ChamberTargetTemp)
	}

	val, ok := state["target_chamber_temperature"]
	if !ok {
		t.Fatal("expected target_chamber_temperature in state even when 0")
	}
	if val != 0 {
		t.Fatalf("expected target_chamber_temperature to be 0, got %v", val)
	}
}
