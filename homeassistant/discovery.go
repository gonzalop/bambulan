package homeassistant

import (
	"encoding/json"
	"fmt"
)

// Device represents the Home Assistant device information.
type Device struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Model        string   `json:"model,omitempty"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	SwVersion    string   `json:"sw_version,omitempty"`
}

// DiscoveryConfig is the base structure for HA MQTT discovery.
type DiscoveryConfig struct {
	Name                string   `json:"name"`
	UniqueID            string   `json:"unique_id"`
	StateTopic          string   `json:"state_topic,omitempty"`
	CommandTopic        string   `json:"command_topic,omitempty"`
	AvailabilityTopic   string   `json:"availability_topic,omitempty"`
	PayloadAvailable    string   `json:"payload_available,omitempty"`
	PayloadNotAvailable string   `json:"payload_not_available,omitempty"`
	PayloadOn           string   `json:"payload_on,omitempty"`
	PayloadOff          string   `json:"payload_off,omitempty"`
	ValueTemplate       string   `json:"value_template,omitempty"`
	UnitOfMeasurement   string   `json:"unit_of_measurement,omitempty"`
	DeviceClass         string   `json:"device_class,omitempty"`
	StateClass          string   `json:"state_class,omitempty"`
	Icon                string   `json:"icon,omitempty"`
	Device              *Device  `json:"device,omitempty"`
	EntityCategory      string   `json:"entity_category,omitempty"`
	Options             []string `json:"options,omitempty"`
	Min                 *float64 `json:"min,omitempty"`
	Max                 *float64 `json:"max,omitempty"`
	Step                *float64 `json:"step,omitempty"`
	Mode                string   `json:"mode,omitempty"`  // "auto", "box" or "slider"
	Topic               string   `json:"topic,omitempty"` // For Camera
}

func (d *DiscoveryConfig) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}

// Entity mapping helpers

func createSensorConfig(prefix, serial, model, displayName, entityID, name, unit, deviceClass, stateClass, icon, category, host string) *DiscoveryConfig {
	return createSensorConfigCustom(prefix, serial, model, displayName, entityID, name, unit, deviceClass, stateClass, icon, category, host, fmt.Sprintf("{{ value_json.%s }}", entityID))
}

func createSensorConfigCustom(prefix, serial, model, displayName, entityID, name, unit, deviceClass, stateClass, icon, category, host, valueTemplate string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:                name,
		UniqueID:            fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:          fmt.Sprintf("%s/%s/state", prefix, tag),
		AvailabilityTopic:   fmt.Sprintf("%s/%s/online/state", prefix, tag),
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		ValueTemplate:       valueTemplate,
		UnitOfMeasurement:   unit,
		DeviceClass:         deviceClass,
		StateClass:          stateClass,
		Icon:                icon,
		EntityCategory:      category,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createBinarySensorConfig(prefix, serial, model, displayName, entityID, name, deviceClass, icon, category, host string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	cfg := &DiscoveryConfig{
		Name:           name,
		UniqueID:       fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:     fmt.Sprintf("%s/%s/%s/state", prefix, tag, entityID),
		PayloadOn:      "ON",
		PayloadOff:     "OFF",
		DeviceClass:    deviceClass,
		Icon:           icon,
		EntityCategory: category,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
	// Don't set availability for the online sensor itself
	if entityID != "online" {
		cfg.AvailabilityTopic = fmt.Sprintf("%s/%s/online/state", prefix, tag)
		cfg.PayloadAvailable = "ON"
		cfg.PayloadNotAvailable = "OFF"
	}
	return cfg
}

func createSwitchConfig(prefix, serial, model, displayName, entityID, name, icon, category, host string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:                name,
		UniqueID:            fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:          fmt.Sprintf("%s/%s/%s/state", prefix, tag, entityID),
		CommandTopic:        fmt.Sprintf("%s/%s/%s/set", prefix, tag, entityID),
		AvailabilityTopic:   fmt.Sprintf("%s/%s/online/state", prefix, tag),
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		PayloadOn:           "ON",
		PayloadOff:          "OFF",
		Icon:                icon,
		EntityCategory:      category,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createButtonConfig(prefix, serial, model, displayName, entityID, name, icon, category, host string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:                name,
		UniqueID:            fmt.Sprintf("%s_%s", tag, entityID),
		CommandTopic:        fmt.Sprintf("%s/%s/%s/set", prefix, tag, entityID),
		AvailabilityTopic:   fmt.Sprintf("%s/%s/online/state", prefix, tag),
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		Icon:                icon,
		EntityCategory:      category,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createSelectConfig(prefix, serial, model, displayName, entityID, name, icon, category, host string, options []string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:                name,
		UniqueID:            fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:          fmt.Sprintf("%s/%s/state", prefix, tag),
		ValueTemplate:       fmt.Sprintf("{{ value_json.%s }}", entityID),
		CommandTopic:        fmt.Sprintf("%s/%s/%s/set", prefix, tag, entityID),
		AvailabilityTopic:   fmt.Sprintf("%s/%s/online/state", prefix, tag),
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		Icon:                icon,
		EntityCategory:      category,
		Options:             options,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createNumberConfig(prefix, serial, model, displayName, entityID, name, unit, deviceClass, icon, category, host string, min, max, step float64) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:                name,
		UniqueID:            fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:          fmt.Sprintf("%s/%s/state", prefix, tag),
		ValueTemplate:       fmt.Sprintf("{{ value_json.%s }}", entityID),
		CommandTopic:        fmt.Sprintf("%s/%s/%s/set", prefix, tag, entityID),
		AvailabilityTopic:   fmt.Sprintf("%s/%s/online/state", prefix, tag),
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		UnitOfMeasurement:   unit,
		DeviceClass:         deviceClass,
		Icon:                icon,
		EntityCategory:      category,
		Min:                 &min,
		Max:                 &max,
		Step:                &step,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createCameraConfig(prefix, serial, model, displayName, entityID, name, category, host string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:                name,
		UniqueID:            fmt.Sprintf("%s_%s", tag, entityID),
		Topic:               fmt.Sprintf("%s/%s/%s/image", prefix, tag, entityID),
		AvailabilityTopic:   fmt.Sprintf("%s/%s/online/state", prefix, tag),
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		EntityCategory:      category,
		Device: &Device{
			Identifiers:  []string{tag},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}
