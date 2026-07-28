package homeassistant

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Device represents the Home Assistant device information using abbreviations.
type Device struct {
	Identifiers  []string `json:"ids"`
	Name         string   `json:"name,omitempty"`
	Model        string   `json:"mdl,omitempty"`
	Manufacturer string   `json:"mf,omitempty"`
	SwVersion    string   `json:"sw,omitempty"`
	SerialNumber string   `json:"sn,omitempty"`
}

// AvailabilityConfig allows specifying multiple availability topics using abbreviated keys.
type AvailabilityConfig struct {
	Topic               string `json:"t"`
	PayloadAvailable    string `json:"pl_avail,omitempty"`
	PayloadNotAvailable string `json:"pl_not_avail,omitempty"`
}

// DiscoveryConfig is the base structure for HA MQTT discovery using abbreviated keys.
type DiscoveryConfig struct {
	BaseTopic           string               `json:"~,omitempty"`
	Name                string               `json:"name,omitempty"`
	ObjectID            string               `json:"obj_id,omitempty"`
	UniqueID            string               `json:"uniq_id"`
	StateTopic          string               `json:"stat_t,omitempty"`
	CommandTopic        string               `json:"cmd_t,omitempty"`
	Availability        []AvailabilityConfig `json:"avty,omitempty"`
	AvailabilityMode    string               `json:"avty_mode,omitempty"`
	AvailabilityTopic   string               `json:"avty_t,omitempty"`
	PayloadAvailable    string               `json:"pl_avail,omitempty"`
	PayloadNotAvailable string               `json:"pl_not_avail,omitempty"`
	PayloadOn           string               `json:"pl_on,omitempty"`
	PayloadOff          string               `json:"pl_off,omitempty"`
	ValueTemplate       string               `json:"val_tpl,omitempty"`
	UnitOfMeasurement   string               `json:"unit_of_meas,omitempty"`
	DeviceClass         string               `json:"dev_cla,omitempty"`
	StateClass          string               `json:"stat_cla,omitempty"`
	Icon                string               `json:"ic,omitempty"`
	Device              *Device              `json:"dev,omitempty"`
	EntityCategory      string               `json:"ent_cat,omitempty"`
	Options             []string             `json:"ops,omitempty"`
	Min                 *float64             `json:"min,omitempty"`
	Max                 *float64             `json:"max,omitempty"`
	Step                *float64             `json:"step,omitempty"`
	Mode                string               `json:"mode,omitempty"`
	Topic               string               `json:"t,omitempty"` // For Camera
}

func (d *DiscoveryConfig) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}

// DiscoveryFactory helps create discovery configurations with shared device info.
type DiscoveryFactory struct {
	Prefix      string
	Serial      string
	Model       string
	DisplayName string
	Device      *Device
}

// DeviceTag returns the standardized MQTT topic tag for a printer serial (e.g. "bambu_lab_01S...").
func DeviceTag(serial string) string {
	return fmt.Sprintf("bambu_lab_%s", serial)
}

func NewDiscoveryFactory(prefix, serial, model, displayName string) *DiscoveryFactory {
	return &DiscoveryFactory{
		Prefix:      prefix,
		Serial:      serial,
		Model:       model,
		DisplayName: displayName,
		Device: &Device{
			Identifiers:  []string{DeviceTag(serial)},
			Name:         displayName,
			Model:        model,
			Manufacturer: "Bambu Lab",
		},
	}
}

func (f *DiscoveryFactory) tag() string {
	return DeviceTag(f.Serial)
}

func (f *DiscoveryFactory) entitySlug(entityID string) string {
	// Slugify the display name (e.g. "Bambu Lab P1S 1267" -> "bambu_lab_p1s_1267")
	slug := strings.ToLower(f.DisplayName)
	slug = strings.ReplaceAll(slug, " ", "_")
	return fmt.Sprintf("%s_%s", slug, entityID)
}

func (f *DiscoveryFactory) baseConfig(entityID, name string) *DiscoveryConfig {
	return &DiscoveryConfig{
		BaseTopic:           fmt.Sprintf("%s/%s", f.Prefix, f.tag()),
		Name:                name,
		ObjectID:            f.entitySlug(entityID),
		UniqueID:            fmt.Sprintf("%s_%s", f.tag(), entityID),
		AvailabilityTopic:   "~/online/state",
		PayloadAvailable:    "ON",
		PayloadNotAvailable: "OFF",
		Device:              f.Device,
	}
}

func (f *DiscoveryFactory) Sensor(entityID, name, unit, devClass, statClass, icon, category string) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.StateTopic = "~/state"
	cfg.ValueTemplate = fmt.Sprintf("{{ value_json.%s }}", entityID)
	cfg.UnitOfMeasurement = unit
	cfg.DeviceClass = devClass
	cfg.StateClass = statClass
	cfg.Icon = icon
	cfg.EntityCategory = category
	return cfg
}

func (f *DiscoveryFactory) BinarySensor(entityID, name, devClass, icon, category string) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.StateTopic = fmt.Sprintf("~/%s/state", entityID)
	cfg.PayloadOn = "ON"
	cfg.PayloadOff = "OFF"
	cfg.DeviceClass = devClass
	cfg.Icon = icon
	cfg.EntityCategory = category
	if entityID == "online" {
		cfg.AvailabilityTopic = "" // Self-availability
	}
	return cfg
}

func (f *DiscoveryFactory) Switch(entityID, name, icon, category string) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.StateTopic = fmt.Sprintf("~/%s/state", entityID)
	cfg.CommandTopic = fmt.Sprintf("~/%s/set", entityID)
	cfg.PayloadOn = "ON"
	cfg.PayloadOff = "OFF"
	cfg.Icon = icon
	cfg.EntityCategory = category
	return cfg
}

func (f *DiscoveryFactory) Button(entityID, name, icon, category string) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.CommandTopic = fmt.Sprintf("~/%s/set", entityID)
	cfg.Icon = icon
	cfg.EntityCategory = category
	return cfg
}

func (f *DiscoveryFactory) Select(entityID, name, icon, category string, options []string) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.StateTopic = "~/state"
	cfg.ValueTemplate = fmt.Sprintf("{{ value_json.%s }}", entityID)
	cfg.CommandTopic = fmt.Sprintf("~/%s/set", entityID)
	cfg.Icon = icon
	cfg.EntityCategory = category
	cfg.Options = options
	return cfg
}

func (f *DiscoveryFactory) Number(entityID, name, unit, devClass, icon, category string, min, max, step float64) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.StateTopic = "~/state"
	cfg.ValueTemplate = fmt.Sprintf("{{ value_json.%s }}", entityID)
	cfg.CommandTopic = fmt.Sprintf("~/%s/set", entityID)
	cfg.UnitOfMeasurement = unit
	cfg.DeviceClass = devClass
	cfg.Icon = icon
	cfg.EntityCategory = category
	cfg.Min = &min
	cfg.Max = &max
	cfg.Step = &step
	return cfg
}

func (f *DiscoveryFactory) Camera(entityID, name, category string) *DiscoveryConfig {
	cfg := f.baseConfig(entityID, name)
	cfg.Topic = fmt.Sprintf("~/%s/image", entityID)
	cfg.EntityCategory = category
	return cfg
}
