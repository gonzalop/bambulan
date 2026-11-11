package homeassistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gonzalop/bambulan"
)

// TestBridgeSharedStateConcurrentAccess exercises the fields touched by MQTT
// callbacks, the event loop and the file-sync goroutine. Run with -race.
func TestBridgeSharedStateConcurrentAccess(_ *testing.T) {
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

func TestAddChamberState(t *testing.T) {
	tests := []struct {
		name       string
		caps       bambulan.PrinterCapability
		target     float64
		wantTarget any // nil means the key must be absent
		wantTemp   bool
	}{
		{"heater, target zero", bambulan.PrinterCapability{HasChamberHeater: true, HasChamberTemp: true}, 0, 0, true},
		{"heater, target set", bambulan.PrinterCapability{HasChamberHeater: true}, 45, 45, false},
		{"temp sensor only", bambulan.PrinterCapability{HasChamberTemp: true}, 45, nil, true},
		{"no chamber", bambulan.PrinterCapability{}, 45, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := map[string]any{}
			addChamberState(state, tt.caps, &bambulan.PrinterStatus{ChamberTargetTemp: tt.target}, 30)

			got, ok := state["target_chamber_temperature"]
			if tt.wantTarget == nil {
				if ok {
					t.Errorf("target_chamber_temperature = %v, want absent", got)
				}
			} else if !ok || got != tt.wantTarget {
				t.Errorf("target_chamber_temperature = %v (present=%v), want %v", got, ok, tt.wantTarget)
			}
			if _, ok := state["chamber_temperature"]; ok != tt.wantTemp {
				t.Errorf("chamber_temperature present = %v, want %v", ok, tt.wantTemp)
			}
		})
	}
}

func TestNewBridgeRequiresSerial(t *testing.T) {
	// The broker address is unreachable on purpose: the check must fail before dialing.
	_, err := NewBridge(bambulan.NewClient("h", "c", ""), "tcp://127.0.0.1:1", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "serial is required") {
		t.Fatalf("NewBridge with empty serial = %v, want 'serial is required'", err)
	}
}
