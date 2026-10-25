package bambulan

import (
	"context"
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
