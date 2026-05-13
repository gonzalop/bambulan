package bambulan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAMSEntryUnmarshalling(t *testing.T) {
	// Simulate JSON response from printer with humidity_raw
	jsonStr := `{
		"ams": {
			"ams": [
				{
					"id": "0",
					"humidity": "1",
					"humidity_raw": "10",
					"temp": "25.0",
					"tray": []
				},
				{
					"id": "1",
					"humidity": "5",
					"humidity_raw": "80",
					"temp": "22.5",
					"tray": []
				}
			]
		}
	}`

	// Unwrap the "print" object logic simulation or just unmarshal into PrinterStatus directly if we skip the wrapper
	// The real logic in mqtt_client.go first unmarshals into a map, then into PrinterStatus.
	// But AMSEntry is inside PrinterStatus -> AMS -> []AMSEntry

	var status PrinterStatus
	// We need to match the structure. The JSON above roughly matches what would be inside the "print" object.
	// Let's wrap it to match the full message structure if needed, or just partial.
	// Actually, PrinterStatus has "ams" field which is *AMS.
	// And *AMS has "ams" field which is []*AMSEntry.

	if err := json.Unmarshal([]byte(jsonStr), &status); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if status.Ams == nil {
		t.Fatal("Expected AMS status to be present")
	}

	if len(status.Ams.Ams) != 2 {
		t.Fatalf("Expected 2 AMS units, got %d", len(status.Ams.Ams))
	}

	// Verify Unit 0
	unit0 := status.Ams.Ams[0]
	if unit0.Humidity != "1" {
		t.Errorf("Unit 0: Expected Humidity '1', got '%s'", unit0.Humidity)
	}
	if unit0.HumidityRaw != "10" {
		t.Errorf("Unit 0: Expected HumidityRaw '10', got '%s'", unit0.HumidityRaw)
	}

	// Verify Unit 1
	unit1 := status.Ams.Ams[1]
	if unit1.Humidity != "5" {
		t.Errorf("Unit 1: Expected Humidity '5', got '%s'", unit1.Humidity)
	}
	if unit1.HumidityRaw != "80" {
		t.Errorf("Unit 1: Expected HumidityRaw '80', got '%s'", unit1.HumidityRaw)
	}
}

func TestHMSDecoding(t *testing.T) {
	t.Run("Valid Code", func(t *testing.T) {
		status := PrinterStatus{
			Hms: []HMSEvent{
				{Code: 0x03000100, Attr: 0x00010003},
			},
		}
		msg := status.HMSMessage()
		expected := "0300-0100-0001-0003: The heatbed temperature is abnormal; the heater is over temperature."
		if msg != expected {
			t.Errorf("Expected HMS message '%s', got '%s'", expected, msg)
		}
	})

	t.Run("Empty HMS", func(t *testing.T) {
		status := PrinterStatus{Hms: []HMSEvent{}}
		if msg := status.HMSMessage(); msg != "" {
			t.Errorf("Expected empty message for no HMS, got '%s'", msg)
		}
	})

	t.Run("Unknown Code", func(t *testing.T) {
		status := PrinterStatus{
			Hms: []HMSEvent{
				{Code: 0xDEADBEEF, Attr: 0xCAFEBABE},
			},
		}
		msg := status.HMSMessage()
		// Should return just the code if description is missing
		expected := "DEAD-BEEF-CAFE-BABE"
		if msg != expected {
			t.Errorf("Expected HMS code only, got '%s'", msg)
		}
	})

	t.Run("Multiple Codes", func(t *testing.T) {
		status := PrinterStatus{
			Hms: []HMSEvent{
				{Code: 0x03000100, Attr: 0x00010003},
				{Code: 0x03001200, Attr: 0x00020001},
			},
		}
		msg := status.HMSMessage()
		if !strings.Contains(msg, "heatbed temperature") || !strings.Contains(msg, "front cover") {
			t.Errorf("Message should contain both errors, got: %s", msg)
		}
	})
}
