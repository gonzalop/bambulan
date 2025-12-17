package bambulan

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	BambuQoS = 0 // Anything else either blocks (for subscriptions) or is ignored (for publications).
)

// MQTTClient handles the MQTT connection to the printer for control and monitoring.
// It manages the connection lifecycle, subscription to status topics, and publishing of commands.
type MQTTClient struct {
	// Hostname is the IP or hostname of the printer's MQTT broker.
	Hostname string
	// AccessCode is the password for the MQTT connection.
	AccessCode string
	// Serial is the printer's serial number, used to construct topic strings (device/<serial>/...).
	Serial string
	// OnUpdate is called whenever a new status report is received from the printer.
	OnUpdate func(*PrinterStatus)

	client mqtt.Client
	status *PrinterStatus
	seq    atomic.Int64
}

// NewMQTTClient creates a new MQTTClient.
//
// Parameters:
//   - hostname: Printer IP/hostname.
//   - accessCode: Printer access code (password).
//   - serial: Printer serial number.
//   - onUpdate: Callback for status updates.
func NewMQTTClient(hostname, accessCode, serial string, onUpdate func(*PrinterStatus)) *MQTTClient {
	client := &MQTTClient{
		Hostname:   hostname,
		AccessCode: accessCode,
		Serial:     serial,
		status:     &PrinterStatus{},
		OnUpdate:   onUpdate,
	}
	// Initialize sequence with timestamp to avoid collisions on restart
	client.seq.Store(time.Now().Unix())
	return client
}

// GetPrinterStatus returns the current status pointer.
func (m *MQTTClient) GetPrinterStatus() *PrinterStatus {
	return m.status
}

func (m *MQTTClient) getNextSequenceID() string {
	return strconv.FormatInt(m.seq.Add(1), 10)
}

// Start connects to the MQTT broker and subscribes to report topics.
func (m *MQTTClient) Start() error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcps://%s:8883", m.Hostname))
	opts.SetUsername("bblp")
	opts.SetPassword(m.AccessCode)
	opts.SetClientID(fmt.Sprintf("bambu-go-%s", m.Serial))
	opts.SetTLSConfig(&tls.Config{
		// Bambu Lab printers use self-signed certificates for their MQTT broker.
		InsecureSkipVerify: true,
	})
	opts.SetAutoReconnect(true)
	opts.SetKeepAlive(10 * time.Second)
	opts.SetPingTimeout(5 * time.Second)
	opts.SetOnConnectHandler(m.onConnect)
	opts.SetDefaultPublishHandler(m.onMessage)

	m.client = mqtt.NewClient(opts)
	if token := m.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	return nil
}

// Stop disconnects from the MQTT broker.
func (m *MQTTClient) Stop() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(250)
	}
}

func (m *MQTTClient) onConnect(client mqtt.Client) {
	topic := fmt.Sprintf("device/%s/report", m.Serial)
	if token := client.Subscribe(topic, BambuQoS, m.onMessage); token.Wait() && token.Error() != nil {
		slog.Error("Error subscribing to topic", "topic", topic, "error", token.Error())
	} else {
		slog.Info("Subscribed to topic", "topic", topic)
		// Request full status update to ensure we have all fields (like lights_report)
		go func() {
			if _, err := m.DumpInfo(); err != nil {
				slog.Error("Failed to dump info on connect", "error", err)
			}
		}()
	}
}

func (m *MQTTClient) onMessage(client mqtt.Client, msg mqtt.Message) {
	var partial bambuMessage
	// Unmarshal wrapper first
	if err := json.Unmarshal(msg.Payload(), &partial); err != nil {
		slog.Error("Error unmarshalling message wrapper", "error", err)
		return
	}

	if partial.Print == nil {
		return
	}

	// 1. Get the raw "print" object from JSON.
	// 2. Unmarshal that raw JSON into m.status

	var rawObj map[string]json.RawMessage
	if err := json.Unmarshal(msg.Payload(), &rawObj); err != nil {
		return
	}

	if printRaw, ok := rawObj["print"]; ok {
		if err := json.Unmarshal(printRaw, m.status); err != nil {
			slog.Error("Error updating status", "error", err)
			return
		}
		slog.Debug("Message received", "raw", printRaw)
		if m.OnUpdate != nil {
			// Invoke callback in a separate goroutine to avoid blocking the MQTT read loop.
			// Pass a shallow copy to minimize race conditions on immediate fields (like SequenceID/Result).
			statusCopy := *m.status
			go m.OnUpdate(&statusCopy)
		}
	}
}

// Publish sends a JSON command to the printer request topic.
func (m *MQTTClient) Publish(command any) error {
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	topic := fmt.Sprintf("device/%s/request", m.Serial)

	// Use QoS 0 for fire-and-forget sending to avoid blocking on handshake.
	// We rely on application-level response (Sequence ID) for confirmation.
	token := m.client.Publish(topic, BambuQoS, false, payload)
	token.Wait()
	return token.Error()
}

// Commands

// DumpInfo requests a full status push from the printer.
// It sends a "pushall" command. The printer will respond by publishing a full status report to the report topic.
// Returns the sequence ID of the request, which can be used to correlate the response.
func (m *MQTTClient) DumpInfo() (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"pushing": map[string]any{
			"sequence_id": seqID,
			"command":     "pushall",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// SendGCode sends a single line of G-Code to the printer.
// Returns the sequence ID of the request.
//
// Example:
//
//	client.MQTT.SendGCode("G28") // Auto-home
func (m *MQTTClient) SendGCode(gcode string) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"command":     "gcode_line",
			"sequence_id": seqID,
			"param":       fmt.Sprintf("%s \n", gcode),
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// StartPrint starts a print job for a file already on the printer (SD card).
// The filename specifies the path to the file on the printer (e.g., "Metadata/plate_1.gcode" or "model.gcode").
// Note: You usually need to upload the file via FTPS first.
//
// Returns the sequence ID of the request.
func (m *MQTTClient) StartPrint(filename string, opts PrintOptions) (string, error) {
	// TODO: the .g3code.3mf files are just zip files. Perhaps we should get the file name from there.
	//   And what about multiple plates? Metadata/model_settings.config has the file and supports
	//   multiple plates.
	param := "Metadata/plate_1.gcode"
	if strings.HasSuffix(strings.ToLower(filename), ".gcode") {
		param = filename
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id":    seqID,
			"command":        "project_file",
			"param":          param,
			"subtask_name":   filename,
			"url":            fmt.Sprintf("ftp://%s", filename),
			"bed_type":       opts.BedType,
			"timelapse":      opts.Timelapse,
			"bed_leveling":   opts.BedLeveling,
			"flow_cali":      opts.FlowCalibration,
			"vibration_cali": opts.VibrationCalibration,
			"layer_inspect":  opts.LayerInspection,
			"use_ams":        opts.UseAMS,
			"profile_id":     "0",
			"project_id":     "0",
			"subtask_id":     "0",
			"task_id":        "0",
		},
	}
	return seqID, m.Publish(cmd)
}

// SetChamberLight turns the chamber light on or off.
func (m *MQTTClient) SetChamberLight(on bool) (string, error) {
	mode := "off"
	if on {
		mode = "on"
	}
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"system": map[string]any{
			"sequence_id":   seqID,
			"command":       "ledctrl",
			"led_node":      "chamber_light",
			"led_mode":      mode,
			"led_on_time":   500,
			"led_off_time":  500,
			"loop_times":    0,
			"interval_time": 0,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// PausePrint pauses the current print job.
func (m *MQTTClient) PausePrint() (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "pause",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// ResumePrint resumes a paused print job.
func (m *MQTTClient) ResumePrint() (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "resume",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// StopPrint cancels and stops the current print job.
func (m *MQTTClient) StopPrint() (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "stop",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// SetSpeedProfile sets the print speed profile.
// Supported levels are defined by Speed* constants in models.go.
func (m *MQTTClient) SetSpeedProfile(level string) (string, error) {
	// level: 1=Silent, 2=Standard, 3=Sport, 4=Ludicrous
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "print_speed",
			"param":       level,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(cmd)
}

// SetAMSFilament updates the filament properties (color and type) for a specific AMS slot.
//
// NOTE: This command is tricky and often doesn't work correctly with the printer's current firmware.
//
// Parameters:
//   - amsID: The ID of the AMS unit (0-indexed, typically 0 for the first AMS).
//   - trayID: The ID of the tray within the AMS unit (0-indexed, 0-3 for each AMS).
//   - color: The filament color in RRGGBBAA hex format (e.g., "FF0000FF" for opaque red).
//   - filamentType: The filament material type identifier (e.g., "PLA Basic", "PETG", "ABS").
//
// Returns the sequence ID of the request.
func (m *MQTTClient) SetAMSFilament(amsID, trayID int, color, filamentType string) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id":  seqID,
			"command":      "ams_filament_setting",
			"ams_id":       amsID,
			"tray_id":      trayID,
			"tray_id_name": filamentType,
			"tray_color":   color,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	fmt.Printf("%v\n", cmd)
	return seqID, m.Publish(cmd)
}
