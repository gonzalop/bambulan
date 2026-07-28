package homeassistant

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDashboardEntitiesMatchPublishedDiscovery(t *testing.T) {
	// Read dashboard template
	data, err := os.ReadFile("dashboard.yaml")
	if err != nil {
		t.Fatalf("Failed to read dashboard.yaml: %v", err)
	}

	content := string(data)

	// Extract all entity references: "entity: <entity_id>"
	re := regexp.MustCompile(`entity:\s+([^\s]+)`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatal("No entity references found in dashboard.yaml")
	}

	// Create DiscoveryFactory for a test model/serial
	model := "p1s"
	serial := "01S00A12345678"
	last4 := "5678"
	displayName := "Bambu Lab P1S " + last4
	factory := NewDiscoveryFactory("homeassistant", serial, model, displayName)

	// List of all discovery configs published by bridge (component -> entityID)
	publishedEntities := make(map[string]bool)

	addEntity := func(component string, cfg *DiscoveryConfig) {
		fullID := component + "." + cfg.ObjectID
		publishedEntities[fullID] = true
	}

	// Base Sensors
	addEntity("sensor", factory.Sensor("print_stage", "Print Stage", "", "", "", "mdi:printer-3d", ""))
	addEntity("sensor", factory.Sensor("subtask_name", "Subtask Name", "", "", "", "mdi:file-text-outline", ""))
	addEntity("sensor", factory.Sensor("progress", "Progress", "%", "", "measurement", "mdi:progress-clock", ""))
	addEntity("sensor", factory.Sensor("remaining_time", "Remaining Time", "min", "duration", "measurement", "mdi:timer-sand", ""))
	addEntity("sensor", factory.Sensor("layer_progress", "Layer Progress", "", "", "", "mdi:layers-triple", ""))
	addEntity("sensor", factory.Sensor("current_layer", "Current Layer", "", "", "", "mdi:layers-triple", "diagnostic"))
	addEntity("sensor", factory.Sensor("total_layers", "Total Layers", "", "", "", "mdi:layers-triple-outline", "diagnostic"))
	addEntity("sensor", factory.Sensor("nozzle_temperature", "Nozzle Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", ""))
	addEntity("sensor", factory.Sensor("nozzle_target_temperature", "Nozzle Target Temperature", "°C", "temperature", "measurement", "mdi:thermometer-chevron-up", "diagnostic"))
	addEntity("sensor", factory.Sensor("bed_temperature", "Bed Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", ""))
	addEntity("sensor", factory.Sensor("bed_target_temperature", "Bed Target Temperature", "°C", "temperature", "measurement", "mdi:thermometer-chevron-up", "diagnostic"))
	addEntity("sensor", factory.Sensor("chamber_temperature", "Chamber Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", ""))
	addEntity("sensor", factory.Sensor("wifi_signal", "WiFi Signal", "dBm", "signal_strength", "measurement", "mdi:wifi", "diagnostic"))
	addEntity("sensor", factory.Sensor("ip_address", "IP Address", "", "", "", "mdi:ip-network", "diagnostic"))
	addEntity("binary_sensor", factory.BinarySensor("online", "Online", "connectivity", "mdi:printer-check", "diagnostic"))
	addEntity("binary_sensor", factory.BinarySensor("hms_error_active", "HMS Error Active", "problem", "mdi:alert-circle", ""))
	addEntity("sensor", factory.Sensor("hms_error_description", "HMS Error Description", "", "", "", "mdi:text-box-search", ""))
	addEntity("switch", factory.Switch("chamber_light", "Chamber Light", "mdi:lightbulb-outline", ""))
	addEntity("switch", factory.Switch("camera_enable", "Camera Streaming", "mdi:video-outline", "diagnostic"))
	addEntity("button", factory.Button("pause_print", "Pause Print", "mdi:pause", ""))
	addEntity("button", factory.Button("resume_print", "Resume Print", "mdi:play", ""))
	addEntity("button", factory.Button("stop_print", "Stop Print", "mdi:stop", ""))
	addEntity("button", factory.Button("refresh_files", "Refresh Files", "mdi:refresh", ""))
	addEntity("select", factory.Select("speed_profile", "Speed Profile", "mdi:speedometer", "", []string{"Silent", "Standard", "Sport", "Ludicrous"}))
	addEntity("select", factory.Select("print_file", "Print File", "mdi:file-send", "", []string{"None"}))
	addEntity("number", factory.Number("target_nozzle_temperature", "Target Nozzle Temperature", "°C", "temperature", "mdi:thermometer-chevron-up", "", 0, 300, 1))
	addEntity("number", factory.Number("target_bed_temperature", "Target Bed Temperature", "°C", "temperature", "mdi:thermometer-chevron-up", "", 0, 110, 1))
	addEntity("number", factory.Number("target_chamber_temperature", "Target Chamber Temperature", "°C", "temperature", "mdi:thermometer-chevron-up", "", 0, 60, 1))
	addEntity("number", factory.Number("part_cooling_fan", "Part Cooling Fan", "", "", "mdi:fan", "", 0, 15, 1))
	addEntity("number", factory.Number("aux_fan", "Aux Fan", "", "", "mdi:fan", "", 0, 15, 1))
	addEntity("number", factory.Number("chamber_fan", "Chamber Fan", "", "", "mdi:fan", "", 0, 15, 1))
	addEntity("camera", factory.Camera("camera", "Camera", ""))

	// Validate all entities in dashboard.yaml match a published entity
	for _, match := range matches {
		rawEntity := match[1]
		// Replace template variables <MODEL> and <LAST_4_SERIAL>
		entityID := strings.ReplaceAll(rawEntity, "<MODEL>", model)
		entityID = strings.ReplaceAll(entityID, "<LAST_4_SERIAL>", last4)

		if !publishedEntities[entityID] {
			t.Errorf("Dashboard entity %q (raw: %q) is not published in Home Assistant discovery!", entityID, rawEntity)
		}
	}
}
