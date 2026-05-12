package bambulan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mq "github.com/gonzalop/mq"
)

// BambuQoS is the Quality of Service level used for Bambu Lab MQTT communication.
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

	client *mq.Client
	status *PrinterStatus
	seq    atomic.Int64

	mu          sync.RWMutex
	subscribers map[int64]chan *PrinterStatus
	nextSubID   int64

	OnConnect    func()
	OnDisconnect func(error)
}

// NewMQTTClient creates a new MQTTClient.
//
// Parameters:
//   - hostname: Printer IP/hostname.
//   - accessCode: Printer access code (password).
//   - serial: Printer serial number.
func NewMQTTClient(hostname, accessCode, serial string) *MQTTClient {
	client := &MQTTClient{
		Hostname:    hostname,
		AccessCode:  accessCode,
		Serial:      serial,
		status:      &PrinterStatus{},
		subscribers: make(map[int64]chan *PrinterStatus),
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
	server := fmt.Sprintf("tcps://%s:8883", m.Hostname)
	opts := []mq.Option{
		mq.WithProtocolVersion(mq.ProtocolV311),
		mq.WithClientID(fmt.Sprintf("bambu-go-%s", m.Serial)),
		mq.WithCredentials("bblp", m.AccessCode),
		mq.WithTLS(&tls.Config{
			// Bambu Lab printers use self-signed certificates for their MQTT broker.
			InsecureSkipVerify: true,
		}),
		mq.WithAutoReconnect(true),
		mq.WithKeepAlive(10 * time.Second),
		mq.WithConnectTimeout(5 * time.Second),
		mq.WithOnConnect(m.onConnect),
		mq.WithLogger(slog.Default()),
		mq.WithOnConnectionLost(m.onConnectionLost),
	}

	var err error
	m.client, err = mq.Dial(server, opts...)
	if err != nil {
		return err
	}

	return nil
}

// Stop disconnects from the MQTT broker.
func (m *MQTTClient) Stop() {
	if m.client != nil && m.client.IsConnected() {
		_ = m.client.Disconnect(context.Background())
	}
}

// EventSubscription represents a subscription to printer status updates.
type EventSubscription struct {
	C      <-chan *PrinterStatus
	id     int64
	client *MQTTClient
}

// Cancel unsubscribes from the updates and cleans up resources.
func (s *EventSubscription) Cancel() {
	s.client.unsubscribe(s.id)
}

// Subscribe creates a new subscription for printer status updates.
// The returned EventSubscription contains a channel that will receive updates.
// Call Cancel() on the subscription when done to free resources.
func (m *MQTTClient) Subscribe() *EventSubscription {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextSubID
	m.nextSubID++

	ch := make(chan *PrinterStatus)
	m.subscribers[id] = ch

	return &EventSubscription{
		C:      ch,
		id:     id,
		client: m,
	}
}

func (m *MQTTClient) unsubscribe(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ch, ok := m.subscribers[id]; ok {
		delete(m.subscribers, id)
		close(ch)
	}
}

func (m *MQTTClient) broadcastStatus() {
	statusCopy := *m.status

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.subscribers {
		select {
		case ch <- &statusCopy:
		default:
			// Client is too slow, drop the update. They will get the next one.
		}
	}
}

func (m *MQTTClient) onConnectionLost(_ *mq.Client, err error) {
	slog.Warn("Connection lost", "error", err)
	if m.OnDisconnect != nil {
		m.OnDisconnect(err)
	}
}

func (m *MQTTClient) onConnect(client *mq.Client) {
	slog.Info("Connection established")
	if m.OnConnect != nil {
		m.OnConnect()
	}
	topic := fmt.Sprintf("device/%s/report", m.Serial)
	token := client.Subscribe(topic, BambuQoS, m.onMessage)
	if err := token.Wait(context.Background()); err != nil {
		slog.Error("Error subscribing to topic", "topic", topic, "error", err)
	} else if token.Error() != nil {
		slog.Error("Error subscribing to topic", "topic", topic, "error", token.Error())
	} else {
		slog.Debug("Subscribed to topic", "topic", topic)

		go func() {
			// Initial fetch uses a dedicated timeout context
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Request version info to detect model (for bed limits)
			if _, err := m.GetVersion(ctx); err != nil {
				slog.Error("Failed to get version on connect", "error", err)
			}
			// Request full status update to ensure we have all fields (like lights_report)
			if _, err := m.DumpInfo(ctx); err != nil {
				slog.Error("Failed to dump info on connect", "error", err)
			}
		}()
	}
}

func (m *MQTTClient) onMessage(_ *mq.Client, msg mq.Message) {
	var partial bambuMessage
	// Unmarshal wrapper first
	if err := json.Unmarshal(msg.Payload, &partial); err != nil {
		slog.Error("Error unmarshalling message wrapper", "error", err)
		return
	}

	if partial.Print == nil && partial.Info == nil {
		return
	}

	// 1. Get the raw "print" object from JSON.
	// 2. Unmarshal that raw JSON into m.status

	var rawObj map[string]json.RawMessage
	if err := json.Unmarshal(msg.Payload, &rawObj); err != nil {
		return
	}

	if printRaw, ok := rawObj["print"]; ok {
		if err := json.Unmarshal(printRaw, m.status); err != nil {
			slog.Error("Error updating status", "error", err)
			return
		}
		// Update derived fields
		m.status.PrintStageDesc = m.status.GetPrintStageName()

		slog.Debug("Message received", "raw", printRaw)
		m.broadcastStatus()
	}

	if infoRaw, ok := rawObj["info"]; ok {
		var info InfoMessage
		if err := json.Unmarshal(infoRaw, &info); err != nil {
			slog.Error("Error parsing info message", "error", err)
			return
		}
		// Process info to extract model and limits
		m.processInfo(&info)

		// We might want to notify update here too, so the UI gets the new limit immediately
		m.broadcastStatus()
	}
}

// Publish sends a JSON command to the printer request topic.
func (m *MQTTClient) Publish(ctx context.Context, command any) error {
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	topic := fmt.Sprintf("device/%s/request", m.Serial)

	// Use QoS 0 for fire-and-forget sending.
	// We rely on application-level response (Sequence ID) for confirmation.
	token := m.client.Publish(topic, payload, mq.WithQoS(BambuQoS))
	// Pass ctx to Wait so we can cancel if needed
	if err := token.Wait(ctx); err != nil {
		return err
	}
	return token.Error()
}

// Commands

// DumpInfo requests a full status push from the printer.
// It sends a "pushall" command. The printer will respond by publishing a full status report to the report topic.
// Returns the sequence ID of the request, which can be used to correlate the response.
func (m *MQTTClient) DumpInfo(ctx context.Context) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"pushing": map[string]any{
			"sequence_id": seqID,
			"command":     "pushall",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// GetVersion requests the printer version info (firmware, model, etc).
func (m *MQTTClient) GetVersion(ctx context.Context) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"info": map[string]any{
			"sequence_id": seqID,
			"command":     "get_version",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SendGCode sends a single line of G-Code to the printer.
// Returns the sequence ID of the request.
//
// Example:
//
//	client.MQTT.SendGCode(ctx, "G28") // Auto-home
func (m *MQTTClient) SendGCode(ctx context.Context, gcode string) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"command":     "gcode_line",
			"sequence_id": seqID,
			"param":       fmt.Sprintf("%s \n", gcode),
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// StartPrint starts a print job for a file already on the printer (SD card).
// The filename specifies the path to the file on the printer (e.g., "model.gcode.3mf").
// Note: You usually need to upload the file via FTPS first.
//
// Returns the sequence ID of the request.
//
// Example:
//
//	// 1. Upload file first (see FileClient.UploadFile)
//	// err := client.File.UploadFile(ctx, "local.3mf", "my-model.gcode.3mf", nil)
//
//	// 2. Start Print
//	opts := bambulan.PrintOptions{
//	    BedType:         "textured_plate",
//	    BedLeveling:     true,
//	    FlowCalibration: true,
//	}
//	seqID, err := client.MQTT.StartPrint(ctx, "my-model.gcode.3mf", opts)
func (m *MQTTClient) StartPrint(ctx context.Context, filename string, opts PrintOptions) (string, error) {
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
	return seqID, m.Publish(ctx, cmd)
}

// SetChamberLight turns the chamber light on or off.
//
// Example:
//
//	// Turn light on
//	client.MQTT.SetChamberLight(ctx, true)
func (m *MQTTClient) SetChamberLight(ctx context.Context, on bool) (string, error) {
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
	return seqID, m.Publish(ctx, cmd)
}

// PausePrint pauses the current print job.
func (m *MQTTClient) PausePrint(ctx context.Context) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "pause",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// ResumePrint resumes a paused print job.
func (m *MQTTClient) ResumePrint(ctx context.Context) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "resume",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// StopPrint cancels and stops the current print job.
func (m *MQTTClient) StopPrint(ctx context.Context) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "stop",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetSpeedProfile sets the print speed profile.
// Supported levels are defined by Speed* constants in models.go.
func (m *MQTTClient) SetSpeedProfile(ctx context.Context, level string) (string, error) {
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
	return seqID, m.Publish(ctx, cmd)
}

// SetAMSFilament updates the filament properties for a specific AMS slot.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - amsID: AMS unit ID (0-3).
//   - trayID: Slot ID (0-3).
//   - filamentID: Filament ID (e.g., "GFA00").
//   - settingID: Setting ID (e.g., "GFA00_1.75_PLA...").
//   - color: RGBA hex color (e.g., "FFFFFFFF").
//   - filamentType: Filament type (e.g., "PLA Basic").
//   - minTemp: Min nozzle temp (e.g., 190).
//   - maxTemp: Max nozzle temp (e.g., 220).
func (m *MQTTClient) SetAMSFilament(ctx context.Context, amsID, trayID int, filamentID, settingID, color, filamentType string, minTemp, maxTemp int) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id":     seqID,
			"command":         "ams_filament_setting",
			"ams_id":          amsID,
			"slot_id":         trayID,
			"tray_id":         trayID,
			"tray_info_idx":   filamentID,
			"setting_id":      settingID,
			"tray_color":      color,
			"tray_type":       filamentType,
			"nozzle_temp_min": minTemp,
			"nozzle_temp_max": maxTemp,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// UnloadFilament sends a command to the printer to unload the current filament.
// This triggers the "unload_filament" printer command.
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) UnloadFilament(ctx context.Context) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "unload_filament",
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// LoadFilament sends a command to load filament from a specific AMS slot.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - target: The slot ID to load from. 0-15 correspond to the 4 slots in up to 4 AMS units.
//     254 typically represents the external spool holder.
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) LoadFilament(ctx context.Context, target int) (string, error) {
	if (target < 0 || target > 15) && target != 254 {
		return "", fmt.Errorf("invalid target: %d (must be 0-15 or 254)", target)
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "ams_change_filament",
			"target":      target,
			"curr_temp":   250,
			"tar_temp":    250,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SendAMSControlCommand sends an AMS control command.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - param: The control parameter, one of "resume", "pause", or "reset".
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SendAMSControlCommand(ctx context.Context, param string) (string, error) {
	allowedParams := map[string]bool{
		"resume": true, "pause": true, "reset": true,
	}
	if !allowedParams[param] {
		return "", fmt.Errorf("invalid param: '%s' (must be resume, pause, or reset)", param)
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "ams_control",
			"param":       param,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetAMSUserSetting updates AMS user settings for a specific unit.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - amsID: The ID of the AMS unit (0-3).
//   - startupReadOption: If true, the AMS will read the RFID on startup.
//   - trayReadOption: If true, the AMS will read the RFID when a tray is inserted.
//   - calibrateRemainFlag: If true, the AMS will calibrate the remaining filament on startup.
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SetAMSUserSetting(ctx context.Context, amsID int, startupReadOption, trayReadOption, calibrateRemainFlag bool) (string, error) {
	if amsID < 0 || amsID > 3 {
		return "", fmt.Errorf("invalid amsID: %d (must be 0-3)", amsID)
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id":           seqID,
			"command":               "ams_user_setting",
			"ams_id":                amsID,
			"startup_read_option":   startupReadOption,
			"tray_read_option":      trayReadOption,
			"calibrate_remain_flag": calibrateRemainFlag,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetSpoolKFactor sets the linear advance K-factor for a specific spool (tray).
//
// Parameters:
//   - ctx: Context for cancellation.
//   - trayID: The ID of the tray (0-15 or 254).
//   - kValue: The K-factor value.
//   - nCoef: The N coefficient (typically 1.4).
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SetSpoolKFactor(ctx context.Context, trayID int, kValue float64, nCoef float64) (string, error) {
	if (trayID < 0 || trayID > 15) && trayID != 254 {
		return "", fmt.Errorf("invalid trayID: %d (must be 0-15 or 254)", trayID)
	}
	// Permissive range for K-factor, but block negative
	if kValue < 0 {
		return "", fmt.Errorf("invalid kValue: %f (must be >= 0)", kValue)
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "extrusion_cali_set",
			"tray_id":     trayID,
			"k_value":     kValue,
			"n_coef":      nCoef,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetPrintOption enables or disables specific printer options.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - option: The option name. Common options include:
//     "auto_recovery", "auto_switch_filament", "filament_tangle_detect", "sound_enable".
//   - enabled: The desired state of the option.
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SetPrintOption(ctx context.Context, option string, enabled bool) (string, error) {
	allowedOptions := map[string]bool{
		"auto_recovery":          true,
		"auto_switch_filament":   true,
		"filament_tangle_detect": true,
		"sound_enable":           true,
	}
	if !allowedOptions[option] {
		return "", fmt.Errorf("invalid option: '%s' (must be e.g. auto_recovery, sound_enable)", option)
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "print_option",
			option:        enabled,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}

	// auto_recovery requires an additional "option" field
	if option == "auto_recovery" {
		val := 0
		if enabled {
			val = 1
		}
		// We'll trust the map logic to handle this correctly as a dynamic map
		cmd["print"].(map[string]any)["option"] = val
	}

	return seqID, m.Publish(ctx, cmd)
}

// SkipObjects skips specific objects during a multi-object print.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - objects: A list of object IDs to skip.
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SkipObjects(ctx context.Context, objects []int) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"print": map[string]any{
			"sequence_id": seqID,
			"command":     "skip_objects",
			"obj_list":    objects,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetBuildPlateMarkerDetector enables or disables the AI build plate marker detector (ArUco).
//
// Parameters:
//   - ctx: Context for cancellation.
//   - enabled: If true, enables the detector.
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SetBuildPlateMarkerDetector(ctx context.Context, enabled bool) (string, error) {
	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"xcam": map[string]any{
			"sequence_id": seqID,
			"command":     "xcam_control_set",
			"control":     enabled,
			"enable":      enabled,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetNozzleDetails configures the printer's nozzle settings.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - diameter: The nozzle diameter in mm (e.g., 0.4).
//   - typeString: The nozzle type (e.g., "hardened_steel", "stainless_steel").
//
// Returns:
//   - The sequence ID of the command.
//   - An error if the command could not be published.
func (m *MQTTClient) SetNozzleDetails(ctx context.Context, diameter float64, typeString string) (string, error) {
	allowedDiameters := map[float64]bool{
		0.2: true, 0.4: true, 0.6: true, 0.8: true,
	}
	if !allowedDiameters[diameter] {
		return "", fmt.Errorf("invalid nozzle diameter: %f (must be 0.2, 0.4, 0.6, or 0.8)", diameter)
	}

	allowedTypes := map[string]bool{
		"hardened_steel":  true,
		"stainless_steel": true,
	}
	if !allowedTypes[typeString] {
		return "", fmt.Errorf("invalid nozzle type: '%s' (must be hardened_steel or stainless_steel)", typeString)
	}

	seqID := m.getNextSequenceID()
	cmd := map[string]any{
		"system": map[string]any{
			"sequence_id":     seqID,
			"command":         "set_accessories",
			"nozzle_diameter": diameter,
			"nozzle_type":     typeString,
		},
		"user_id": "1234567890", // dummy user ID required by the printer's MQTT protocol
	}
	return seqID, m.Publish(ctx, cmd)
}

// SetFanSpeed sets the speed of the specified fan(s).
// It sends the appropriate M106 G-code command.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - fan: The fan to control. One of "part" (P1), "aux" (P2), "chamber" (P3), or "all".
//   - percent: The target speed percentage (0-100).
//
// Returns:
//   - The sequence ID of the G-code command.
//   - An error if the command could not be sent or the fan type is invalid.
//
// Example:
//
//	client.MQTT.SetFanSpeed(ctx, "aux", 100) // Set auxiliary fan to 100%
func (m *MQTTClient) SetFanSpeed(ctx context.Context, fan string, percent int) (string, error) {
	// Validate percentage
	if percent < 0 || percent > 100 {
		return "", fmt.Errorf("invalid fan speed: %d (must be 0-100)", percent)
	}

	// Calculate S value (0-255)
	// 255 * (percent / 100)
	sVal := int(float64(percent) * 2.55)

	var gcode string
	switch fan {
	case "part":
		gcode = fmt.Sprintf("M106 P1 S%d\n", sVal)
	case "aux":
		gcode = fmt.Sprintf("M106 P2 S%d\n", sVal)
	case "chamber":
		gcode = fmt.Sprintf("M106 P3 S%d\n", sVal)
	case "all":
		gcode = fmt.Sprintf("M106 P1 S%d\nM106 P2 S%d\nM106 P3 S%d\n", sVal, sVal, sVal)
	default:
		return "", fmt.Errorf("invalid fan type: %s (must be part, aux, chamber, or all)", fan)
	}

	return m.SendGCode(ctx, gcode)
}

// SetBedTemperature sets the target bed temperature using M140 G-code.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - temp: The target temperature in Celsius.
//
// Returns:
//   - The sequence ID of the G-code command.
//   - An error if the command could not be sent.
//
// Example:
//
//	client.MQTT.SetBedTemperature(ctx, 60) // Set bed to 60°C
func (m *MQTTClient) SetBedTemperature(ctx context.Context, temp int) (string, error) {
	limit := m.status.BedTempLimit
	if limit == 0 {
		limit = 100 // Default safe limit if unknown
	}
	if temp < 0 || temp > limit {
		return "", fmt.Errorf("invalid bed temperature: %d (must be 0-%d)", temp, limit)
	}
	gcode := fmt.Sprintf("M140 S%d\n", temp)
	return m.SendGCode(ctx, gcode)
}

// SetNozzleTemperature sets the target nozzle (tool) temperature using M104 G-code.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - temp: The target temperature in Celsius.
//   - toolIdx: The index of the tool (extruder) to set (optional, use 0 for single).
//
// Returns:
//   - The sequence ID of the G-code command.
//   - An error if the command could not be sent.
//
// Example:
//
//	client.MQTT.SetNozzleTemperature(ctx, 220, 0) // Set nozzle 1 to 220°C
func (m *MQTTClient) SetNozzleTemperature(ctx context.Context, temp int, toolIdx int) (string, error) {
	limit := m.status.NozzleTempLimit
	if limit == 0 {
		limit = 300 // Default safe limit
	}
	if temp < 0 || temp > limit {
		return "", fmt.Errorf("invalid nozzle temperature: %d (must be 0-%d)", temp, limit)
	}

	gcode := fmt.Sprintf("M104 S%d\n", temp)
	if toolIdx > 0 {
		gcode = fmt.Sprintf("M104 T%d S%d\n", toolIdx, temp)
	}
	return m.SendGCode(ctx, gcode)
}

// SetChamberTemperature sets the target chamber temperature using M191 G-code.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - temp: The target temperature in Celsius.
//
// Returns:
//   - The sequence ID of the G-code command.
//   - An error if the command could not be sent.
//
// Example:
//
//	client.MQTT.SetChamberTemperature(ctx, 50) // Set chamber to 50°C
func (m *MQTTClient) SetChamberTemperature(ctx context.Context, temp int) (string, error) {
	caps := GetPrinterCapabilities(m.status.DeviceModel)
	if !caps.HasChamberHeater {
		return "", fmt.Errorf("printer does not support chamber heating")
	}
	limit := caps.MaxChamberTemp
	if temp < 0 || temp > limit {
		return "", fmt.Errorf("invalid chamber temperature: %d (must be 0-%d)", temp, limit)
	}
	gcode := fmt.Sprintf("M191 S%d\n", temp)
	return m.SendGCode(ctx, gcode)
}

func (m *MQTTClient) processInfo(info *InfoMessage) {
	if info.Command != "get_version" {
		return
	}

	var model string

	// Store all modules in status for CLI display
	m.status.Modules = info.Module

	// Try to find model from OTA module
	for _, mod := range info.Module {
		if mod.Name == "ota" && mod.Project != "" {
			model = mod.Project
			break
		}
	}

	// Fallback: Derive from Serial Number
	if model == "" && len(m.Serial) >= 3 {
		prefix := m.Serial[:3]
		// Map known prefixes to Model IDs used in getBedTempLimit
		switch prefix {
		case "00M", "00W":
			model = "BL-P001" // X1C
		case "001":
			model = "BL-P002" // X1
		case "01S":
			model = "C11" // P1P
		case "01P":
			model = "C12" // P1S
		case "030", "03N":
			model = "N1" // A1 Mini
		case "01N":
			model = "N2S" // A1
		default:
			slog.Info("Unknown serial prefix", "prefix", prefix)
		}
	}

	if model != "" {
		m.status.DeviceModel = model
		m.status.BedTempLimit = getBedTempLimit(model)
		m.status.NozzleTempLimit = getNozzleTempLimit(model)
		slog.Debug("Device detected", "model", model, "bed_limit", m.status.BedTempLimit)
	}
}

func getBedTempLimit(model string) int {
	printerCap := GetPrinterCapabilities(model)
	if printerCap.MaxBedTemp > 0 {
		return printerCap.MaxBedTemp
	}
	// Fallback safe limit
	return 100
}

func getNozzleTempLimit(model string) int {
	printerCap := GetPrinterCapabilities(model)
	if printerCap.MaxNozzleTemp > 0 {
		return printerCap.MaxNozzleTemp
	}
	// Fallback safe limit
	return 300
}
