package bambulan

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gonzalop/mq"
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

func TestOnMessageBroadcastsOnlyForPresentSections(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    int
	}{
		{"print only", `{"print":{"nozzle_temper":200}}`, 1},
		{"info only", `{"info":{"command":"get_version"}}`, 1},
		{"neither", `{"system":{}}`, 0},
		{"print null", `{"print":null}`, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMQTTClient("host", "code", "serial")
			ch := make(chan *PrinterStatus, 10)
			m.subscribers[0] = ch

			m.onMessage(nil, mq.Message{Payload: []byte(tc.payload)})

			if got := len(ch); got != tc.want {
				t.Errorf("broadcasts = %d; want %d", got, tc.want)
			}
		})
	}
}

func TestMQTTClient_StatusDataRace(_ *testing.T) {
	client := NewMQTTClient("127.0.0.1", "12345678", "0123456789")
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			payload := []byte(`{"print":{"gcode_state":"RUNNING","bed_temper":60.0,"lights_report":[{"node":"chamber_light","mode":"on"}]}}`)
			client.onMessage(nil, mq.Message{Payload: payload})
		}()
		go func() {
			defer wg.Done()
			st := client.GetPrinterStatus()
			if st != nil && len(st.LightsReport) > 0 {
				_ = st.LightsReport[0].Mode
			}
		}()
		go func() {
			defer wg.Done()
			_, _ = client.SetChamberLight(context.Background(), true)
		}()
	}
	wg.Wait()
}

func TestMQTTClient_SubscribeBuffering(t *testing.T) {
	client := NewMQTTClient("127.0.0.1", "12345678", "0123456789")
	sub := client.Subscribe()
	defer sub.Cancel()

	// Verify channel buffer capacity
	if cap(sub.C) < 16 {
		t.Fatalf("expected sub.C buffer capacity >= 16, got %d", cap(sub.C))
	}

	// Broadcast multiple messages without immediately reading, ensuring no drops
	for i := 0; i < 5; i++ {
		client.onMessage(nil, mq.Message{Payload: []byte(`{"print":{"mc_percent":10}}`)})
	}

	if len(sub.C) != 5 {
		t.Fatalf("expected 5 buffered messages in sub.C, got %d", len(sub.C))
	}
}

func TestPrinterStatus_Clone(t *testing.T) {
	orig := &PrinterStatus{
		DeviceModel: "BL-P001",
		BedTemp:     65.0,
		LightsReport: []*LightsReport{
			{Node: "chamber_light", Mode: "on"},
		},
		Ams: &AMS{
			Version: 1,
			Ams: []*AMSEntry{
				{
					ID: "0",
					Tray: []*VTTray{
						{ID: "0", TrayColor: "FF0000FF"},
					},
				},
			},
		},
	}

	cp := orig.Clone()
	if cp == nil {
		t.Fatal("expected clone not to be nil")
	}

	// Mutate copy
	cp.BedTemp = 100.0
	cp.LightsReport[0].Mode = "off"
	cp.Ams.Ams[0].Tray[0].TrayColor = "00FF00FF"

	// Verify original is untouched
	if orig.BedTemp != 65.0 {
		t.Errorf("original BedTemp mutated: got %v, want 65.0", orig.BedTemp)
	}
	if orig.LightsReport[0].Mode != "on" {
		t.Errorf("original LightsReport mutated: got %v, want 'on'", orig.LightsReport[0].Mode)
	}
	if orig.Ams.Ams[0].Tray[0].TrayColor != "FF0000FF" {
		t.Errorf("original TrayColor mutated: got %v, want 'FF0000FF'", orig.Ams.Ams[0].Tray[0].TrayColor)
	}
}

// Run with -race: a subscriber cancelling during a broadcast used to panic
// with "send on closed channel".
func TestBroadcastWhileUnsubscribing(_ *testing.T) {
	m := NewMQTTClient("h", "c", "S")
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		sub := m.Subscribe()
		wg.Add(2)
		go func() { defer wg.Done(); m.broadcastStatus() }()
		go func() { defer wg.Done(); sub.Cancel() }()
	}
	wg.Wait()
}

func TestPublishWithoutConnection(t *testing.T) {
	m := NewMQTTClient("h", "c", "S")
	if err := m.Publish(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Publish on unstarted client = %v, want 'not connected' error", err)
	}
}

func TestSetChamberTemperatureLimits(t *testing.T) {
	limit := GetPrinterCapabilities("C13").MaxChamberTemp
	tests := []struct {
		name    string
		model   string
		temp    int
		wantErr string // "not connected" means validation passed and it tried to send
	}{
		{"no chamber heater", "C11", 40, "does not support"},
		{"negative", "C13", -1, "invalid chamber temperature"},
		{"at limit", "C13", limit, "not connected"},
		{"over limit", "C13", limit + 1, "invalid chamber temperature"},
		{"zero turns it off", "C13", 0, "not connected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMQTTClient("h", "c", "S")
			m.status.DeviceModel = tt.model
			_, err := m.SetChamberTemperature(context.Background(), tt.temp)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("SetChamberTemperature(%d) on %s = %v, want error containing %q", tt.temp, tt.model, err, tt.wantErr)
			}
		})
	}
}

func TestSetChamberLightKeepsStateWhenPublishFails(t *testing.T) {
	m := NewMQTTClient("h", "c", "S")
	m.status.LightsReport = []*LightsReport{{Node: "chamber_light", Mode: "off"}, {Node: "work_light", Mode: "on"}}

	if _, err := m.SetChamberLight(context.Background(), true); err == nil {
		t.Fatal("SetChamberLight on unstarted client succeeded")
	}
	got := m.GetPrinterStatus().LightsReport
	if len(got) != 2 || got[0].Mode != "off" || got[1].Mode != "on" {
		t.Errorf("lights changed after failed publish: %+v %+v", got[0], got[1])
	}
}

func TestSetLightModeKeepsOtherLights(t *testing.T) {
	lights := []*LightsReport{{Node: "work_light", Mode: "on"}, {Node: "chamber_light", Mode: "off"}}
	lights = setLightMode(lights, "chamber_light", "on")
	if len(lights) != 2 || lights[0].Mode != "on" || lights[1].Mode != "on" {
		t.Errorf("setLightMode = %+v %+v", lights[0], lights[1])
	}
	if added := setLightMode(nil, "chamber_light", "on"); len(added) != 1 || added[0].Node != "chamber_light" {
		t.Errorf("setLightMode on empty = %+v", added)
	}
}

// A field with the wrong type must not stop the rest of the update or the broadcast.
func TestOnMessageAppliesRestOnFieldTypeError(t *testing.T) {
	m := NewMQTTClient("h", "c", "S")
	ch := make(chan *PrinterStatus, 1)
	m.subscribers[0] = ch

	m.onMessage(nil, mq.Message{Payload: []byte(`{"print":{"nozzle_temper":"hot","bed_temper":60}}`)})

	if len(ch) != 1 {
		t.Fatal("no broadcast after a field type error")
	}
	if got := (<-ch).BedTemp; got != 60 {
		t.Errorf("BedTemp = %v, want 60 (rest of the update applied)", got)
	}
}
