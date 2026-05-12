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
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	StateTopic        string   `json:"state_topic"`
	CommandTopic      string   `json:"command_topic,omitempty"`
	PayloadOn         string   `json:"payload_on,omitempty"`
	PayloadOff        string   `json:"payload_off,omitempty"`
	ValueTemplate     string   `json:"value_template,omitempty"`
	UnitOfMeasurement string   `json:"unit_of_measurement,omitempty"`
	DeviceClass       string   `json:"device_class,omitempty"`
	StateClass        string   `json:"state_class,omitempty"`
	Icon              string   `json:"icon,omitempty"`
	Device            *Device  `json:"device,omitempty"`
	EntityCategory    string   `json:"entity_category,omitempty"`
	Options           []string `json:"options,omitempty"`
}

func (d *DiscoveryConfig) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}

// Entity mapping helper
func createSensorConfig(prefix, serial, model, entityID, name, unit, deviceClass, stateClass, icon, host string) *DiscoveryConfig {
	return createSensorConfigCustom(prefix, serial, model, entityID, name, unit, deviceClass, stateClass, icon, host, fmt.Sprintf("{{ value_json.%s }}", entityID))
}

func createSensorConfigCustom(prefix, serial, model, entityID, name, unit, deviceClass, stateClass, icon, host, valueTemplate string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:              fmt.Sprintf("%s %s", model, name),
		UniqueID:          fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:        fmt.Sprintf("%s/sensor/%s/state", prefix, tag),
		ValueTemplate:     valueTemplate,
		UnitOfMeasurement: unit,
		DeviceClass:       deviceClass,
		StateClass:        stateClass,
		Icon:              icon,
		Device: &Device{
			Identifiers:  []string{tag, host},
			Name:         fmt.Sprintf("Bambu %s", model),
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createBinarySensorConfig(prefix, serial, model, entityID, name, deviceClass, icon, host string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:        fmt.Sprintf("%s %s", model, name),
		UniqueID:    fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:  fmt.Sprintf("%s/binary_sensor/%s/%s/state", prefix, tag, entityID),
		PayloadOn:   "ON",
		PayloadOff:  "OFF",
		DeviceClass: deviceClass,
		Icon:        icon,
		Device: &Device{
			Identifiers:  []string{tag, host},
			Name:         fmt.Sprintf("Bambu %s", model),
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func createSwitchConfig(prefix, serial, model, entityID, name, icon, host string) *DiscoveryConfig {
	tag := fmt.Sprintf("bambu_%s", serial)
	return &DiscoveryConfig{
		Name:         fmt.Sprintf("%s %s", model, name),
		UniqueID:     fmt.Sprintf("%s_%s", tag, entityID),
		StateTopic:   fmt.Sprintf("%s/switch/%s/%s/state", prefix, tag, entityID),
		CommandTopic: fmt.Sprintf("%s/switch/%s/%s/set", prefix, tag, entityID),
		PayloadOn:    "ON",
		PayloadOff:   "OFF",
		Icon:         icon,
		Device: &Device{
			Identifiers:  []string{tag, host},
			Name:         fmt.Sprintf("Bambu %s", model),
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}
