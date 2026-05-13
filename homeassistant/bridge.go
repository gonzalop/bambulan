package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gonzalop/mq"

	"github.com/gonzalop/bambulan"
)

// Bridge handles the connection between a BambuLAN client and a Home Assistant MQTT broker.
type Bridge struct {
	bambu  *bambulan.Client
	ha     *mq.Client
	prefix string

	host        string
	serial      string
	model       string
	haModel     string
	displayName string
	discoveryOk bool
	entitiesOk  map[string]bool // unique_id -> bool
	files       []string
	lastLight   string
	onlineTopic string

	cameraEnabled bool
}

// NewBridge creates a new Home Assistant MQTT bridge.
func NewBridge(bambu *bambulan.Client, broker, user, pass, prefix string) (*Bridge, error) {
	if prefix == "" {
		prefix = "homeassistant"
	}

	opts := []mq.Option{
		mq.WithCredentials(user, pass),
		mq.WithProtocolVersion(mq.ProtocolV50),
		mq.WithKeepAlive(30 * time.Second),
		mq.WithAutoReconnect(true),
		mq.WithLogger(slog.Default()),
	}

	client, err := mq.Dial(broker, opts...)
	if err != nil {
		return nil, err
	}

	b := &Bridge{
		bambu:      bambu,
		ha:         client,
		prefix:     prefix,
		host:       bambu.MQTT.Hostname,
		serial:     bambu.MQTT.Serial,
		entitiesOk: make(map[string]bool),
		files:      []string{"None"},
		lastLight:  "UNKNOWN",
	}

	// Setup connection callbacks
	bambu.MQTT.OnConnect = func() {
		slog.Debug("Printer-to-BambuLAN connection established")
		if b.serial != "" {
			b.publishOnline(true)
		}
	}
	bambu.MQTT.OnDisconnect = func(err error) {
		slog.Warn("Printer-to-BambuLAN connection lost", "error", err)
		if b.serial != "" {
			b.publishOnline(false)
		}
	}

	return b, nil
}

func (b *Bridge) publishOnline(online bool) {
	if b.serial == "" {
		return
	}
	tag := fmt.Sprintf("bambu_%s", b.serial)
	topic := fmt.Sprintf("%s/%s/online/state", b.prefix, tag)

	state := "OFF"
	if online {
		state = "ON"
	}
	slog.Debug("HA Bridge: Publishing printer online status", "topic", topic, "state", state)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token := b.ha.Publish(topic, []byte(state), mq.WithRetain(true))
	if err := token.Wait(ctx); err != nil {
		slog.Error("HA Bridge: Failed to publish online status", "error", err)
	}
}

func (b *Bridge) publishCameraState() {
	if b.serial == "" {
		return
	}
	tag := fmt.Sprintf("bambu_%s", b.serial)
	topic := fmt.Sprintf("%s/%s/camera_enable/state", b.prefix, tag)

	state := "OFF"
	if b.cameraEnabled {
		state = "ON"
	}
	slog.Debug("HA Bridge: Publishing camera state", "topic", topic, "state", state)
	_ = b.ha.Publish(topic, []byte(state), mq.WithRetain(true))
}

// Start runs the bridge event loop.
func (b *Bridge) Start(ctx context.Context) error {
	slog.Debug("Starting Home Assistant bridge loop")

	sub := b.bambu.Subscribe()
	defer sub.Cancel()

	// Initial check for serial
	if b.serial == "" {
		b.serial = b.bambu.MQTT.Serial
	}

	if b.serial != "" {
		slog.Debug("Printer serial known on start", "serial", b.serial)
		_ = b.setupSubscriptions(ctx)
		b.publishCameraState()
		if b.bambu.MQTT.IsConnected() {
			b.publishOnline(true)
		}
	}

	// File sync loop
	go func() {
		slog.Debug("Starting file sync loop")
		// Wait for discovery to be ready before first sync
		for !b.discoveryOk {
			time.Sleep(2 * time.Second)
			if ctx.Err() != nil {
				return
			}
		}

		// Initial sync
		slog.Debug("Performing initial file sync for Home Assistant")
		if err := b.syncFiles(ctx); err != nil {
			slog.Error("Initial file sync failed", "error", err)
		}

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := b.syncFiles(ctx); err != nil {
					slog.Error("Periodic file sync failed", "error", err)
				}
			}
		}
	}()

	// Camera snapshot loop
	go func() {
		slog.Debug("Starting camera snapshot loop")
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if b.discoveryOk && b.cameraEnabled {
					img, err := b.bambu.Camera.CaptureFrame()
					if err == nil {
						tag := fmt.Sprintf("bambu_%s", b.serial)
						topic := fmt.Sprintf("%s/%s/camera/image", b.prefix, tag)
						_ = b.ha.Publish(topic, img)
					} else {
						slog.Debug("Failed to capture camera frame for HA", "error", err)
					}
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case status := <-sub.C:
			if status == nil {
				continue
			}

			// Update serial if it was unknown
			if b.serial == "" && b.bambu.MQTT.Serial != "" {
				b.serial = b.bambu.MQTT.Serial
				slog.Debug("Printer serial discovered from status", "serial", b.serial)
				_ = b.setupSubscriptions(ctx)
				b.publishCameraState()
				if b.bambu.MQTT.IsConnected() {
					b.publishOnline(true)
				}
			}

			if b.serial == "" {
				// Can't do anything without a serial
				continue
			}

			// 1. Discovery (only once when we have enough info OR after a timeout)
			if !b.discoveryOk {
				b.model = status.DeviceModel
				if b.model == "" {
					b.model = "Printer" // Placeholder
				}

				caps := bambulan.GetPrinterCapabilities(b.model)
				modelName := caps.DisplayName
				if modelName == "" {
					modelName = b.model
				}

				// Simplify model name for HA (e.g. "Bambu Lab P1S" -> "P1S")
				b.haModel = modelName
				b.haModel = strings.TrimPrefix(b.haModel, "Bambu Lab ")
				b.haModel = strings.TrimPrefix(b.haModel, "Bambu ")

				// Determine a unique display name for Home Assistant
				// If DevName is empty or just the internal model ID, use the friendly name + serial suffix
				b.displayName = status.DevName
				if b.displayName == "" || b.displayName == b.model || b.displayName == "Printer" {
					// Use last 4 digits of serial for uniqueness
					suffix := b.serial
					if len(suffix) > 4 {
						suffix = suffix[len(suffix)-4:]
					}
					b.displayName = fmt.Sprintf("%s %s", b.haModel, suffix)
				}

				if err := b.publishDiscovery(b.haModel); err != nil {
					slog.Error("Failed to publish HA discovery", "error", err)
				} else {
					b.discoveryOk = true
					slog.Debug("Home Assistant discovery published successfully", "device", b.displayName, "model", b.haModel)
					// Initial file sync now that discovery is ok
					go func() {
						_ = b.syncFiles(ctx)
					}()
				}
			}

			// 2. State Update
			if b.discoveryOk {
				// Dynamic AMS discovery
				if status.Ams != nil {
					_ = b.publishAMSDiscovery(status)
				}

				if err := b.publishState(status); err != nil {
					slog.Error("Failed to publish HA state", "error", err)
				}
			}
		}
	}
}

func (b *Bridge) publishAMSDiscovery(status *bambulan.PrinterStatus) error {
	if status.Ams == nil {
		return nil
	}

	caps := bambulan.GetPrinterCapabilities(b.model)
	var configs []*DiscoveryConfig

	for i, unit := range status.Ams.Ams {
		if unit == nil {
			continue
		}

		unitID := fmt.Sprintf("ams_%d", i)
		if !b.entitiesOk[unitID+"_humidity"] {
			configs = append(configs, createSensorConfig(b.prefix, b.serial, b.haModel, b.displayName, unitID+"_humidity", fmt.Sprintf("AMS %d Humidity", i), "", "", "measurement", "mdi:water-percent", "", b.host))
			b.entitiesOk[unitID+"_humidity"] = true
		}

		for j, tray := range unit.Tray {
			if tray == nil {
				continue
			}
			trayID := fmt.Sprintf("ams_%d_slot_%d", i, j)

			// Always discover filament type
			if !b.entitiesOk[trayID+"_filament"] {
				configs = append(configs, createSensorConfigCustom(b.prefix, b.serial, b.haModel, b.displayName, trayID+"_filament", fmt.Sprintf("AMS %d Slot %d Filament", i, j+1), "", "", "", "mdi:format-list-bulleted-type", "", b.host, fmt.Sprintf("{{ value_json.%s }}", trayID+"_filament")))
				b.entitiesOk[trayID+"_filament"] = true
			}

			// Only discover 'remain' if the printer supports it AND it reports a valid value (>=0)
			if caps.HasAMSCapacityReporting && tray.Remain >= 0 {
				if !b.entitiesOk[trayID+"_remain"] {
					configs = append(configs, createSensorConfigCustom(b.prefix, b.serial, b.haModel, b.displayName, trayID+"_remain", fmt.Sprintf("AMS %d Slot %d Remaining", i, j+1), "%", "", "measurement", "mdi:gauge", "", b.host, fmt.Sprintf("{{ value_json.%s }}", trayID+"_remain")))
					b.entitiesOk[trayID+"_remain"] = true
				}
			}
		}
	}

	for _, cfg := range configs {
		// Standard flat discovery topic: prefix/component/unique_id/config
		topic := fmt.Sprintf("%s/sensor/%s/config", b.prefix, cfg.UniqueID)
		payload, err := cfg.ToJSON()
		if err != nil {
			return err
		}
		slog.Debug("Publishing AMS discovery", "topic", topic)
		token := b.ha.Publish(topic, payload, mq.WithRetain(true))
		_ = token.Wait(context.Background())
	}

	return nil
}

func (b *Bridge) setupSubscriptions(ctx context.Context) error {
	tag := fmt.Sprintf("bambu_%s", b.serial)
	slog.Debug("Setting up Home Assistant command subscriptions", "tag", tag)

	subscribe := func(topic string, cb func(*mq.Client, mq.Message)) error {
		slog.Debug("Subscribing to HA topic", "topic", topic)
		token := b.ha.Subscribe(topic, 0, cb)
		if err := token.Wait(ctx); err != nil {
			return err
		}
		return token.Error()
	}

	// Chamber Light
	if err := subscribe(fmt.Sprintf("%s/%s/chamber_light/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		on := strings.ToUpper(string(msg.Payload)) == "ON"
		slog.Debug("HA Command received: Chamber Light", "on", on)
		_, _ = b.bambu.MQTT.SetChamberLight(context.Background(), on)
	}); err != nil {
		return err
	}

	// Print Actions
	_ = subscribe(fmt.Sprintf("%s/%s/pause_print/set", b.prefix, tag), func(_ *mq.Client, _ mq.Message) {
		slog.Debug("HA Command received: Pause Print")
		_, _ = b.bambu.MQTT.PausePrint(context.Background())
	})
	_ = subscribe(fmt.Sprintf("%s/%s/resume_print/set", b.prefix, tag), func(_ *mq.Client, _ mq.Message) {
		slog.Debug("HA Command received: Resume Print")
		_, _ = b.bambu.MQTT.ResumePrint(context.Background())
	})
	_ = subscribe(fmt.Sprintf("%s/%s/stop_print/set", b.prefix, tag), func(_ *mq.Client, _ mq.Message) {
		slog.Debug("HA Command received: Stop Print")
		_, _ = b.bambu.MQTT.StopPrint(context.Background())
	})

	// Refresh Files
	_ = subscribe(fmt.Sprintf("%s/%s/refresh_files/set", b.prefix, tag), func(_ *mq.Client, _ mq.Message) {
		slog.Debug("HA Command received: Refresh Files")
		_ = b.syncFiles(ctx)
	})

	// Camera Enable
	_ = subscribe(fmt.Sprintf("%s/%s/camera_enable/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		on := strings.ToUpper(string(msg.Payload)) == "ON"
		slog.Debug("HA Command received: Camera Enable", "on", on)
		b.cameraEnabled = on
		b.publishCameraState()
	})

	// Speed Profile
	_ = subscribe(fmt.Sprintf("%s/%s/speed_profile/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		val := string(msg.Payload)
		slog.Debug("HA Command received: Speed Profile", "value", val)
		level := "2"
		switch val {
		case "Silent":
			level = "1"
		case "Standard":
			level = "2"
		case "Sport":
			level = "3"
		case "Ludicrous":
			level = "4"
		}
		_, _ = b.bambu.MQTT.SetSpeedProfile(context.Background(), level)
	})

	// Print File
	_ = subscribe(fmt.Sprintf("%s/%s/print_file/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		file := string(msg.Payload)
		if file == "None" || file == "" {
			return
		}
		slog.Debug("HA Command received: Print File", "file", file)
		opts := bambulan.PrintOptions{
			BedType:     "textured_plate",
			BedLeveling: true,
		}
		_, _ = b.bambu.MQTT.StartPrint(context.Background(), file, opts)
	})

	// Temperatures
	_ = subscribe(fmt.Sprintf("%s/%s/target_nozzle_temp/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		temp, _ := strconv.Atoi(string(msg.Payload))
		slog.Debug("HA Command received: Target Nozzle Temp", "value", temp)
		caps := bambulan.GetPrinterCapabilities(b.model)
		if temp > caps.MaxNozzleTemp {
			temp = caps.MaxNozzleTemp
		}
		_, _ = b.bambu.MQTT.SetNozzleTemperature(context.Background(), temp, 0)
	})
	_ = subscribe(fmt.Sprintf("%s/%s/target_bed_temp/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		temp, _ := strconv.Atoi(string(msg.Payload))
		slog.Debug("HA Command received: Target Bed Temp", "value", temp)
		caps := bambulan.GetPrinterCapabilities(b.model)
		if temp > caps.MaxBedTemp {
			temp = caps.MaxBedTemp
		}
		_, _ = b.bambu.MQTT.SetBedTemperature(context.Background(), temp)
	})
	_ = subscribe(fmt.Sprintf("%s/%s/target_chamber_temp/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		temp, _ := strconv.Atoi(string(msg.Payload))
		slog.Debug("HA Command received: Target Chamber Temp", "value", temp)
		caps := bambulan.GetPrinterCapabilities(b.model)
		if caps.HasChamberHeater {
			if temp > caps.MaxChamberTemp {
				temp = caps.MaxChamberTemp
			}
			_, _ = b.bambu.MQTT.SetChamberTemperature(context.Background(), temp)
		}
	})

	// Fans
	_ = subscribe(fmt.Sprintf("%s/%s/fan_part/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		val, _ := strconv.Atoi(string(msg.Payload))
		slog.Debug("HA Command received: Fan Part", "value", val)
		// Scale 0-15 to 0-100%
		percent := (val * 100) / 15
		_, _ = b.bambu.MQTT.SetFanSpeed(context.Background(), "part", percent)
	})
	_ = subscribe(fmt.Sprintf("%s/%s/fan_aux/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		val, _ := strconv.Atoi(string(msg.Payload))
		slog.Debug("HA Command received: Fan Aux", "value", val)
		// Scale 0-15 to 0-100%
		percent := (val * 100) / 15
		_, _ = b.bambu.MQTT.SetFanSpeed(context.Background(), "aux", percent)
	})
	_ = subscribe(fmt.Sprintf("%s/%s/fan_chamber/set", b.prefix, tag), func(_ *mq.Client, msg mq.Message) {
		val, _ := strconv.Atoi(string(msg.Payload))
		slog.Debug("HA Command received: Fan Chamber", "value", val)
		// Scale 0-15 to 0-100%
		percent := (val * 100) / 15
		_, _ = b.bambu.MQTT.SetFanSpeed(context.Background(), "chamber", percent)
	})

	return nil
}

func (b *Bridge) syncFiles(ctx context.Context) error {
	slog.Debug("Syncing SD card files for HA selection")
	files, err := b.bambu.File.GetFiles(ctx, "/", ".3mf")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		files = []string{"None"}
	}
	b.files = files
	slog.Debug("Synced files from SD card", "count", len(files))

	// Re-publish discovery to update options
	return b.publishDiscovery(b.haModel)
}

func (b *Bridge) publishDiscovery(model string) error {
	slog.Debug("HA Bridge: Publishing entity discovery configurations", "serial", b.serial)
	caps := bambulan.GetPrinterCapabilities(b.model)

	type entry struct {
		cfg       *DiscoveryConfig
		objectID  string
		component string
	}
	var configs []entry

	// Base Sensors
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "print_stage", "Print Stage", "", "", "", "mdi:printer-3d", "", b.host), "print_stage", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "print_progress", "Progress", "%", "", "measurement", "mdi:progress-clock", "", b.host), "print_progress", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "remaining_time", "Remaining Time", "min", "duration", "measurement", "mdi:timer-sand", "", b.host), "remaining_time", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "nozzle_temp", "Nozzle Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", "", b.host), "nozzle_temp", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "nozzle_target_temp", "Nozzle Target Temperature", "°C", "temperature", "measurement", "mdi:thermometer-chevron-up", "diagnostic", b.host), "nozzle_target_temp", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "bed_temp", "Bed Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", "", b.host), "bed_temp", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "bed_target_temp", "Bed Target Temperature", "°C", "temperature", "measurement", "mdi:thermometer-chevron-up", "diagnostic", b.host), "bed_target_temp", "sensor"})

	if caps.HasChamberTemp {
		configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "chamber_temp", "Chamber Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", "", b.host), "chamber_temp", "sensor"})
	}

	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "wifi_signal", "WiFi Signal", "dBm", "signal_strength", "measurement", "mdi:wifi", "diagnostic", b.host), "wifi_signal", "sensor"})
	configs = append(configs, entry{createSensorConfig(b.prefix, b.serial, model, b.displayName, "ip_address", "IP Address", "", "", "", "mdi:ip-network", "diagnostic", b.host), "ip_address", "sensor"})

	// Binary Sensors
	configs = append(configs, entry{createBinarySensorConfig(b.prefix, b.serial, model, b.displayName, "online", "Online", "connectivity", "mdi:printer-check", "diagnostic", b.host), "online", "binary_sensor"})

	// Switches
	configs = append(configs, entry{createSwitchConfig(b.prefix, b.serial, model, b.displayName, "chamber_light", "Chamber Light", "mdi:lightbulb-outline", "", b.host), "chamber_light", "switch"})
	configs = append(configs, entry{createSwitchConfig(b.prefix, b.serial, model, b.displayName, "camera_enable", "Camera Streaming", "mdi:video-outline", "diagnostic", b.host), "camera_enable", "switch"})

	// Buttons
	configs = append(configs, entry{b.createActionButtonConfig(model, "pause_print", "Pause Print", "mdi:pause"), "pause_print", "button"})
	configs = append(configs, entry{b.createActionButtonConfig(model, "resume_print", "Resume Print", "mdi:play"), "resume_print", "button"})
	configs = append(configs, entry{b.createActionButtonConfig(model, "stop_print", "Stop Print", "mdi:stop"), "stop_print", "button"})
	configs = append(configs, entry{createButtonConfig(b.prefix, b.serial, model, b.displayName, "refresh_files", "Refresh Files", "mdi:refresh", "", b.host), "refresh_files", "button"})

	// Selects
	configs = append(configs, entry{createSelectConfig(b.prefix, b.serial, model, b.displayName, "speed_profile", "Speed Profile", "mdi:speedometer", "", b.host, []string{"Silent", "Standard", "Sport", "Ludicrous"}), "speed_profile", "select"})
	configs = append(configs, entry{createSelectConfig(b.prefix, b.serial, model, b.displayName, "print_file", "Print File", "mdi:file-send", "", b.host, b.files), "print_file", "select"})

	// Numbers
	configs = append(configs, entry{createNumberConfig(b.prefix, b.serial, model, b.displayName, "target_nozzle_temp", "Target Nozzle Temperature", "°C", "temperature", "mdi:thermometer-chevron-up", "", b.host, 0, float64(caps.MaxNozzleTemp), 1), "target_nozzle_temp", "number"})
	configs = append(configs, entry{createNumberConfig(b.prefix, b.serial, model, b.displayName, "target_bed_temp", "Target Bed Temperature", "°C", "temperature", "mdi:thermometer-chevron-up", "", b.host, 0, float64(caps.MaxBedTemp), 1), "target_bed_temp", "number"})

	if caps.HasChamberHeater {
		configs = append(configs, entry{createNumberConfig(b.prefix, b.serial, model, b.displayName, "target_chamber_temp", "Target Chamber Temperature", "°C", "temperature", "mdi:thermometer-chevron-up", "", b.host, 0, float64(caps.MaxChamberTemp), 1), "target_chamber_temp", "number"})
	}

	configs = append(configs, entry{createNumberConfig(b.prefix, b.serial, model, b.displayName, "fan_part", "Part Cooling Fan", "", "", "mdi:fan", "", b.host, 0, 15, 1), "fan_part", "number"})
	if caps.HasAuxFan {
		configs = append(configs, entry{createNumberConfig(b.prefix, b.serial, model, b.displayName, "fan_aux", "Aux Fan", "", "", "mdi:fan", "", b.host, 0, 15, 1), "fan_aux", "number"})
	}
	if caps.HasChamberFan {
		configs = append(configs, entry{createNumberConfig(b.prefix, b.serial, model, b.displayName, "fan_chamber", "Chamber Fan", "", "", "mdi:fan", "", b.host, 0, 15, 1), "fan_chamber", "number"})
	}

	// Camera
	configs = append(configs, entry{createCameraConfig(b.prefix, b.serial, model, b.displayName, "camera", "Camera", "", b.host), "camera", "camera"})

	for _, entry := range configs {
		// Flat discovery topic structure for maximum compatibility: prefix/component/unique_id/config
		topic := fmt.Sprintf("%s/%s/%s/config", b.prefix, entry.component, entry.cfg.UniqueID)
		payload, err := entry.cfg.ToJSON()
		if err != nil {
			return err
		}

		slog.Debug("HA Bridge: Publishing entity discovery", "component", entry.component, "objectID", entry.objectID, "topic", topic)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		token := b.ha.Publish(topic, payload, mq.WithRetain(true))
		err = token.Wait(ctx)
		cancel()
		if err != nil {
			slog.Error("HA Bridge: Failed to publish discovery for entity", "objectID", entry.objectID, "error", err)
			return err
		}
	}

	return nil
}

func (b *Bridge) createActionButtonConfig(model, entityID, name, icon string) *DiscoveryConfig {
	cfg := createButtonConfig(b.prefix, b.serial, model, b.displayName, entityID, name, icon, "", b.host)
	tag := fmt.Sprintf("bambu_%s", b.serial)
	// Use a dedicated availability topic for the action
	cfg.AvailabilityTopic = fmt.Sprintf("%s/%s/%s/availability", b.prefix, tag, entityID)
	cfg.PayloadAvailable = "online"
	cfg.PayloadNotAvailable = "offline"
	return cfg
}

func (b *Bridge) publishState(status *bambulan.PrinterStatus) error {
	tag := fmt.Sprintf("bambu_%s", b.serial)
	speed := "Standard"
	switch status.SpdLvl {
	case 1:
		speed = "Silent"
	case 2:
		speed = "Standard"
	case 3:
		speed = "Sport"
	case 4:
		speed = "Ludicrous"
	}

	state := map[string]any{
		"print_stage":        status.GetPrintStageName(),
		"print_progress":     status.McPercent,
		"remaining_time":     status.McRemainingTime,
		"nozzle_temp":        status.NozzleTemp,
		"nozzle_target_temp": status.NozzleTargetTemp,
		"bed_temp":           status.BedTemp,
		"bed_target_temp":    status.BedTargetTemp,
		"chamber_temp":       status.ChamberTemp,
		"ip_address":         b.host,
		"speed_profile":      speed,
		"target_nozzle_temp": int(status.NozzleTargetTemp),
		"target_bed_temp":    int(status.BedTargetTemp),
	}

	if status.ChamberTargetTemp > 0 {
		state["target_chamber_temp"] = int(status.ChamberTargetTemp)
	}

	// Parse fan speeds (they come as strings like "100" or percentage)
	state["fan_part"] = parseFanSpeed(status.CoolingFanSpeed)
	state["fan_aux"] = parseFanSpeed(status.BigFan1Speed)
	state["fan_chamber"] = parseFanSpeed(status.BigFan2Speed)

	wifi := strings.TrimSpace(strings.TrimSuffix(status.WifiSignal, "dBm"))
	if wifi != "" {
		state["wifi_signal"] = wifi
	}

	// AMS State
	if status.Ams != nil {
		for i, unit := range status.Ams.Ams {
			if unit == nil {
				continue
			}
			state[fmt.Sprintf("ams_%d_humidity", i)] = unit.Humidity
			for j, tray := range unit.Tray {
				if tray == nil {
					continue
				}
				filament := tray.TrayType
				if filament == "" {
					filament = "Empty"
				}
				state[fmt.Sprintf("ams_%d_slot_%d_filament", i, j)] = filament
				state[fmt.Sprintf("ams_%d_slot_%d_remain", i, j)] = tray.Remain
			}
		}
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}

	topic := fmt.Sprintf("%s/%s/state", b.prefix, tag)
	slog.Debug("HA Bridge: Publishing consolidated state", "topic", topic)
	token := b.ha.Publish(topic, payload)
	if err := token.Wait(context.Background()); err != nil {
		return err
	}

	// Update action availability based on printer state
	gcodeState := strings.ToUpper(status.GcodeState)
	// Default to offline unless conditions met
	pauseAvail := "offline"
	resumeAvail := "offline"
	stopAvail := "offline"

	switch gcodeState {
	case "RUNNING", "PREPARE":
		pauseAvail = "online"
		stopAvail = "online"
	case "PAUSE":
		resumeAvail = "online"
		stopAvail = "online"
	}

	b.publishActionAvailability("pause_print", pauseAvail)
	b.publishActionAvailability("resume_print", resumeAvail)
	b.publishActionAvailability("stop_print", stopAvail)

	// Chamber Light
	if len(status.LightsReport) > 0 {
		lightState := "OFF"
		for _, l := range status.LightsReport {
			if l.Node == "chamber_light" && l.Mode == "on" {
				lightState = "ON"
				break
			}
		}
		if lightState != b.lastLight {
			lightTopic := fmt.Sprintf("%s/%s/chamber_light/state", b.prefix, tag)
			slog.Debug("HA Bridge: Publishing light state update", "topic", lightTopic, "state", lightState)
			b.ha.Publish(lightTopic, []byte(lightState))
			b.lastLight = lightState
		}
	}

	return nil
}

func (b *Bridge) publishActionAvailability(entityID, state string) {
	tag := fmt.Sprintf("bambu_%s", b.serial)
	topic := fmt.Sprintf("%s/%s/%s/availability", b.prefix, tag, entityID)
	_ = b.ha.Publish(topic, []byte(state), mq.WithRetain(true))
}

func (b *Bridge) Close() {
	if b.ha != nil {
		_ = b.ha.Disconnect(context.Background())
	}
}

func parseFanSpeed(s string) int {
	s = strings.TrimSuffix(s, "%")
	val, _ := strconv.Atoi(s)
	return val
}
