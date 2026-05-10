package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	displayName string
	discoveryOk bool
	amsOk       map[string]bool // unique_id -> bool
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

	return &Bridge{
		bambu:  bambu,
		ha:     client,
		prefix: prefix,
		host:   bambu.MQTT.Hostname,
		amsOk:  make(map[string]bool),
	}, nil
}

// Start runs the bridge event loop.
func (b *Bridge) Start(ctx context.Context) error {
	sub := b.bambu.Subscribe()
	defer sub.Cancel()

	// Handle incoming commands from HA
	b.serial = b.bambu.MQTT.Serial
	b.setupSubscriptions()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case status := <-sub.C:
			if status == nil {
				continue
			}

			// 1. Discovery (only once when we have enough info)
			if !b.discoveryOk && (status.DeviceModel != "" || len(status.Modules) > 0) {
				b.model = status.DeviceModel
				if b.model == "" {
					b.model = "Unknown"
				}

				// Determine a unique display name for Home Assistant
				b.displayName = status.DevName
				if b.displayName == "" {
					caps := bambulan.GetPrinterCapabilities(b.model)
					modelName := caps.DisplayName
					if modelName == "" {
						modelName = b.model
					}
					// Use last 4 digits of serial for uniqueness
					suffix := b.serial
					if len(suffix) > 4 {
						suffix = suffix[len(suffix)-4:]
					}
					b.displayName = fmt.Sprintf("%s %s", modelName, suffix)
				}

				if err := b.publishDiscovery(); err != nil {
					slog.Error("Failed to publish HA discovery", "error", err)
				} else {
					b.discoveryOk = true
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

	var configs []*DiscoveryConfig

	for i, unit := range status.Ams.Ams {
		if unit == nil {
			continue
		}

		unitID := fmt.Sprintf("ams_%d", i)
		if !b.amsOk[unitID] {
			configs = append(configs, createSensorConfig(b.prefix, b.serial, b.displayName, unitID+"_humidity", fmt.Sprintf("AMS %d Humidity", i), "", "", "", "mdi:water-percent", b.host))
			b.amsOk[unitID] = true
		}

		for j, tray := range unit.Tray {
			if tray == nil {
				continue
			}
			trayID := fmt.Sprintf("ams_%d_slot_%d", i, j)
			if !b.amsOk[trayID] {
				configs = append(configs, createSensorConfigCustom(b.prefix, b.serial, b.displayName, trayID+"_filament", fmt.Sprintf("AMS %d Slot %d Filament", i, j+1), "", "", "", "mdi:format-list-bulleted-type", b.host, fmt.Sprintf("{{ value_json.%s }}", trayID+"_filament")))
				configs = append(configs, createSensorConfigCustom(b.prefix, b.serial, b.displayName, trayID+"_remain", fmt.Sprintf("AMS %d Slot %d Remaining", i, j+1), "%", "", "measurement", "mdi:gauge", b.host, fmt.Sprintf("{{ value_json.%s }}", trayID+"_remain")))
				b.amsOk[trayID] = true
			}
		}
	}

	for _, cfg := range configs {
		topic := fmt.Sprintf("%s/sensor/%s/config", b.prefix, cfg.UniqueID)
		payload, err := cfg.ToJSON()
		if err != nil {
			return err
		}
		token := b.ha.Publish(topic, payload, mq.WithRetain(true))
		_ = token.Wait(context.Background())
	}

	return nil
}

func (b *Bridge) setupSubscriptions() {
	tag := fmt.Sprintf("bambu_%s", b.serial)

	// Chamber Light
	topic := fmt.Sprintf("%s/switch/%s/chamber_light/set", b.prefix, tag)
	b.ha.Subscribe(topic, 0, func(_ *mq.Client, msg mq.Message) {
		payload := string(msg.Payload)
		on := strings.ToUpper(payload) == "ON"
		slog.Info("HA Command: Chamber Light", "on", on)
		_, _ = b.bambu.MQTT.SetChamberLight(context.Background(), on)
	})
}

func (b *Bridge) publishDiscovery() error {
	slog.Info("Publishing Home Assistant discovery configurations", "name", b.displayName, "serial", b.serial)

	configs := []*DiscoveryConfig{
		// Sensors
		createSensorConfig(b.prefix, b.serial, b.displayName, "print_stage", "Print Stage", "", "", "", "mdi:printer-3d", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "print_progress", "Progress", "%", "", "measurement", "mdi:progress-clock", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "remaining_time", "Remaining Time", "min", "duration", "measurement", "mdi:timer-sand", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "nozzle_temp", "Nozzle Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "nozzle_target_temp", "Nozzle Target Temperature", "°C", "temperature", "measurement", "mdi:thermometer-chevron-up", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "bed_temp", "Bed Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "bed_target_temp", "Bed Target Temperature", "°C", "temperature", "measurement", "mdi:thermometer-chevron-up", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "chamber_temp", "Chamber Temperature", "°C", "temperature", "measurement", "mdi:thermometer-lines", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "wifi_signal", "WiFi Signal", "dBm", "signal_strength", "measurement", "mdi:wifi", b.host),
		createSensorConfig(b.prefix, b.serial, b.displayName, "ip_address", "IP Address", "", "", "", "mdi:ip-network", b.host),

		// Switches
		createSwitchConfig(b.prefix, b.serial, b.displayName, "chamber_light", "Chamber Light", "mdi:lightbulb-outline", b.host),
	}

	for _, cfg := range configs {
		component := "sensor"
		if cfg.CommandTopic != "" {
			component = "switch"
		}

		topic := fmt.Sprintf("%s/%s/%s/config", b.prefix, component, cfg.UniqueID)
		payload, err := cfg.ToJSON()
		if err != nil {
			return err
		}

		token := b.ha.Publish(topic, payload, mq.WithRetain(true))
		if err := token.Wait(context.Background()); err != nil {
			return err
		}
	}

	return nil
}

func (b *Bridge) publishState(status *bambulan.PrinterStatus) error {
	tag := fmt.Sprintf("bambu_%s", b.serial)
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
	}

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

	topic := fmt.Sprintf("%s/sensor/%s/state", b.prefix, tag)
	token := b.ha.Publish(topic, payload)
	if err := token.Wait(context.Background()); err != nil {
		return err
	}

	// Also publish switch state separately to its state topic
	lightState := "OFF"
	for _, l := range status.LightsReport {
		if l.Node == "chamber_light" && l.Mode == "on" {
			lightState = "ON"
			break
		}
	}
	lightTopic := fmt.Sprintf("%s/switch/%s/chamber_light/state", b.prefix, tag)
	b.ha.Publish(lightTopic, []byte(lightState))

	return nil
}

func (b *Bridge) Close() {
	if b.ha != nil {
		_ = b.ha.Disconnect(context.Background())
	}
}
