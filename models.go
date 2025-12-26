package bambulan

import "fmt"

// bambuMessage represents the top-level JSON structure received from the printer.
// It is used internally to unwrap the "print" object.
type bambuMessage struct {
	Print *PrinterStatus `json:"print,omitempty"`
	Info  *InfoMessage   `json:"info,omitempty"`
}

type InfoMessage struct {
	Command    string       `json:"command"`
	SequenceID string       `json:"sequence_id"`
	Module     []ModuleInfo `json:"module"`
	Result     string       `json:"result"`
	Reason     string       `json:"reason"`
}

type ModuleInfo struct {
	Name    string `json:"name"`
	Project string `json:"project_name"` // e.g. "C11", "C12" (This is often the model!)
	SwVer   string `json:"sw_ver"`
	HwVer   string `json:"hw_ver"`
	Sn      string `json:"sn"`
}

// PrinterStatus contains the detailed status of the printer components.
// It is the primary data structure returned by the printer via MQTT.
type PrinterStatus struct {
	// Upload contains status information about current file uploads (FTPS).
	Upload *Upload `json:"upload,omitempty"`

	// DeviceModel is the printer's model ID (e.g., "BL-P001", "C11"). Use this with GetPrinterCapabilities.
	DeviceModel string `json:"device_model,omitempty"`

	// Modules lists the hardware and software versions of printer components.
	Modules []ModuleInfo `json:"modules,omitempty"`

	// BedTempLimit is the hardware-enforced maximum temperature for the heatbed.
	BedTempLimit int `json:"bed_temp_limit,omitempty"`

	// NozzleTempLimit is the hardware-enforced maximum temperature for the nozzle.
	NozzleTempLimit int `json:"nozzle_temp_limit,omitempty"`

	// NozzleTemp is the current actual nozzle temperature in Celsius.
	NozzleTemp float64 `json:"nozzle_temper"`

	// NozzleTargetTemp is the target nozzle temperature in Celsius.
	NozzleTargetTemp float64 `json:"nozzle_target_temper"`

	// BedTemp is the current actual bed temperature in Celsius.
	BedTemp float64 `json:"bed_temper"`

	// BedTargetTemp is the target bed temperature in Celsius.
	BedTargetTemp float64 `json:"bed_target_temper"`

	// ChamberTemp is the current actual chamber temperature in Celsius.
	// Note: Not all printers have a chamber temperature sensor.
	ChamberTemp float64 `json:"chamber_temper"`

	// McPrintStage is the internal numeric code for the current print stage.
	// Use GetPrintStageName() to get a human-readable description.
	McPrintStage string `json:"mc_print_stage"`

	// PrintStageDesc is a human-readable description of the print stage, if available.
	PrintStageDesc string `json:"print_stage_desc,omitempty"`

	// HeatbreakFanSpeed is the speed of the heatbreak fan (percentage string or numeric string).
	HeatbreakFanSpeed string `json:"heatbreak_fan_speed"`

	// CoolingFanSpeed is the speed of the part cooling fan (percentage string or numeric string).
	CoolingFanSpeed string `json:"cooling_fan_speed"`

	// BigFan1Speed is the speed of the auxiliary fan (percentage string or numeric string).
	BigFan1Speed string `json:"big_fan1_speed"`

	// BigFan2Speed is the speed of the chamber fan (percentage string or numeric string).
	BigFan2Speed string `json:"big_fan2_speed"`

	// McPercent is the integer percentage of print progress (0-100).
	McPercent int `json:"mc_percent"`

	// McRemainingTime is the estimated remaining print time in minutes.
	McRemainingTime int `json:"mc_remaining_time"`

	// AMSStatus represents the global status of the AMS system (if connected).
	AMSStatus int `json:"ams_status"`

	// AMSRFIDStatus represents the status of RFID reading in the AMS.
	AMSRFIDStatus int `json:"ams_rfid_status"`

	// HwSwitchState is a bitmask representing various hardware switch states.
	HwSwitchState int `json:"hw_switch_state"`

	// SpdMag is the speed multiplier magnitude (e.g., 100 for Standard).
	SpdMag int `json:"spd_mag"`

	// SpdLvl is the current speed profile level:
	// 1=Silent, 2=Standard, 3=Sport, 4=Ludicrous.
	SpdLvl int `json:"spd_lvl"`

	// PrintError contains the error code if the print has failed or encountered an issue.
	PrintError int `json:"print_error"`

	// Lifecycle indicates the high-level state of the printer (e.g., "printing", "idle").
	Lifecycle string `json:"lifecycle"`

	// WifiSignal represents the WiFi signal strength in dBm.
	WifiSignal string `json:"wifi_signal"`

	// GcodeState indicates the G-code execution state (e.g., "RUNNING", "PAUSE", "IDLE", "FINISH").
	GcodeState string `json:"gcode_state"`

	// GcodeFilePreparePercent is the progress of processing the G-code file before printing.
	GcodeFilePreparePercent string `json:"gcode_file_prepare_percent"`

	// QueueNumber serves to identify the print job in the queue.
	QueueNumber int `json:"queue_number"`
	QueueTotal  int `json:"queue_total"`
	QueueEst    int `json:"queue_est"`
	QueueSts    int `json:"queue_sts"`

	// ProjectID identifies the cloud project associated with the print.
	ProjectID string `json:"project_id"`
	ProfileID string `json:"profile_id"`
	TaskID    string `json:"task_id"`

	// SubtaskID identifies the specific print job (subtask).
	SubtaskID string `json:"subtask_id"`
	// SubtaskName is the name of the file or job being printed.
	SubtaskName string `json:"subtask_name"`

	// GcodeFile is the path to the G-code file being printed.
	GcodeFile string `json:"gcode_file"`

	Stg               []any  `json:"stg"`
	StgCur            int    `json:"stg_cur"`
	PrintType         string `json:"print_type"`
	HomeFlag          int    `json:"home_flag"`
	McPrintLineNumber string `json:"mc_print_line_number"`
	McPrintSubStage   int    `json:"mc_print_sub_stage"`

	// Sdcard indicates if an SD card is inserted.
	Sdcard              bool   `json:"sdcard"`
	ForceUpgrade        bool   `json:"force_upgrade"`
	MessProductionState string `json:"mess_production_state"`

	// LayerNum is the current layer being printed.
	LayerNum int `json:"layer_num"`
	// TotalLayerNum is the total number of layers in the G-code file.
	TotalLayerNum int `json:"total_layer_num"`

	SObj         []any           `json:"s_obj"`
	FanGear      int             `json:"fan_gear"`
	Hms          []any           `json:"hms"`
	Online       *Online         `json:"online,omitempty"`
	Ams          *AMS            `json:"ams,omitempty"`
	IPCam        *IPCam          `json:"ipcam,omitempty"`
	VtTray       *VTTray         `json:"vt_tray,omitempty"`
	LightsReport []*LightsReport `json:"lights_report,omitempty"`
	UpgradeState *UpgradeState   `json:"upgrade_state,omitempty"`
	Command      string          `json:"command"`
	Msg          int             `json:"msg"`
	SequenceID   string          `json:"sequence_id"`
	Result       string          `json:"result"`
	Reason       string          `json:"reason"`
}

// Upload represents the progress and status of a file upload to the printer via FTPS.
type Upload struct {
	FileSize      int    `json:"file_size"`
	FinishSize    int    `json:"finish_size"`
	Status        string `json:"status"`   // e.g., "idle", "running", "success"
	Progress      int    `json:"progress"` // Percentage 0-100
	Message       string `json:"message"`
	OSSURL        string `json:"oss_url"`
	SequenceID    string `json:"sequence_id"`
	Speed         int    `json:"speed"` // Upload speed in bytes/sec
	TaskID        string `json:"task_id"`
	TimeRemaining int    `json:"time_remaining"` // Estimated seconds remaining
	TroubleID     string `json:"trouble_id"`
}

// Online indicates the connection status of various printer modules.
type Online struct {
	Ahb     bool `json:"ahb"`     // Automatic Hub Board
	Ext     bool `json:"ext"`     // Extruder Board
	Rfid    bool `json:"rfid"`    // AMS RFID Reader
	Version int  `json:"version"` // Protocol version
}

// VTTray represents a single filament tray in the AMS.
type VTTray struct {
	Id            string   `json:"id"`            // Tray ID (0-3)
	TagUid        string   `json:"tag_uid"`       // RFID Tag UID
	TrayIdName    string   `json:"tray_id_name"`  // User-assigned name
	TrayInfoIdx   string   `json:"tray_info_idx"` // Filament profile ID (e.g. "GFA00")
	TrayType      string   `json:"tray_type"`     // Filament type (e.g. "PLA Basic")
	TraySubBrands string   `json:"tray_sub_brands"`
	TrayColor     string   `json:"tray_color"`    // Color in RGBA hex (e.g. "FFFFFFFF")
	TrayWeight    string   `json:"tray_weight"`   // Estimated weight in grams
	TrayDiameter  string   `json:"tray_diameter"` // Firmware estimate of diameter
	TrayTemp      string   `json:"tray_temp"`     // Recommended temperature range
	TrayTime      string   `json:"tray_time"`     // Usage time?
	BedTempType   string   `json:"bed_temp_type"`
	BedTemp       string   `json:"bed_temp"` // Recommended bed temp
	NozzleTempMax string   `json:"nozzle_temp_max"`
	NozzleTempMin string   `json:"nozzle_temp_min"`
	XcamInfo      string   `json:"xcam_info"`
	TrayUuid      string   `json:"tray_uuid"`
	Remain        int      `json:"remain"` // Remaining percentage estimate
	K             float64  `json:"k"`      // Flow calibration K-factor
	N             int      `json:"n"`      // Flow calibration N-coefficient
	CaliIdx       int      `json:"cali_idx"`
	Cols          []string `json:"cols"`
	Ctype         int      `json:"ctype"`
	DryingTemp    string   `json:"drying_temp"`
	DryingTime    string   `json:"drying_time"`
}

// AMSEntry represents a single AMS unit, which can hold up to 4 trays.
type AMSEntry struct {
	Humidity string    `json:"humidity"` // Humidity level (1-5, where 5 is driest)
	Id       string    `json:"id"`       // AMS Unit ID (0-3)
	Temp     string    `json:"temp"`     // Temperature inside AMS
	Tray     []*VTTray `json:"tray"`     // List of up to 4 trays
}

// AMS conveys the state of the AMS (Automatic Material System).
// A printer can have up to 4 AMS units chained together.
type AMS struct {
	Ams              []*AMSEntry `json:"ams"`
	AmsExistBits     string      `json:"ams_exist_bits"`
	AmsExistBitsRaw  string      `json:"ams_exist_bits_raw"`
	TrayExistBits    string      `json:"tray_exist_bits"`
	TrayIsBblBits    string      `json:"tray_is_bbl_bits"` // Bitmask for Bambu Lab rfid trays
	TrayTar          string      `json:"tray_tar"`         // Target tray being loaded
	TrayNow          string      `json:"tray_now"`         // Currently loaded tray
	TrayPre          string      `json:"tray_pre"`
	TrayReadDoneBits string      `json:"tray_read_done_bits"`
	TrayReadingBits  string      `json:"tray_reading_bits"`
	Version          int         `json:"version"`
	InsertFlag       bool        `json:"insert_flag"` // True if filament is detected in the hub
	PowerOnFlag      bool        `json:"power_on_flag"`
}

// IPCam contains information about the printer's camera stream.
type IPCam struct {
	AgoraService string `json:"agora_service"`
	IPCamDev     string `json:"ipcam_dev"`
	IPCamRecord  string `json:"ipcam_record"`
	Timelapse    string `json:"timelapse"`
	Resolution   string `json:"resolution"` // e.g., "1080p", "720p"
	TutkServer   string `json:"tutk_server"`
	ModeBits     int    `json:"mode_bits"`
	RTSPURL      string `json:"rtsp_url"` // rtsp:// or rtsps:// URL for live stream
}

// LightsReport contains the status of the printer's lighting.
type LightsReport struct {
	Node string `json:"node"` // e.g., "chamber_light", "work_light"
	Mode string `json:"mode"` // "on", "off", "flashing"
}

// UpgradeState tracks the firmware upgrade process.
type UpgradeState struct {
	SequenceID          int    `json:"sequence_id"`
	Progress            string `json:"progress"` // 0-100 percentage string
	Status              string `json:"status"`   // "IDLE", "DOWNLOADING", "FLASHING", "SUCCESS", "FAILED"
	ConsistencyRequest  bool   `json:"consistency_request"`
	DisState            int    `json:"dis_state"`
	ErrCode             int    `json:"err_code"`
	ForceUpgrade        bool   `json:"force_upgrade"`
	Message             string `json:"message"`
	Module              string `json:"module"` // The component being upgraded
	NewVersionState     int    `json:"new_version_state"`
	NewVerList          []any  `json:"new_ver_list"`
	CurStateCode        int    `json:"cur_state_code"`
	AhbNewVersionNumber string `json:"ahb_new_version_number"`
	AmsNewVersionNumber string `json:"ams_new_version_number"`
	ExtNewVersionNumber string `json:"ext_new_version_number"`
	Idx                 int    `json:"idx"`
	Idx1                int    `json:"idx1"`
	LowerLimit          string `json:"lower_limit"`
	OtaNewVersionNumber string `json:"ota_new_version_number"`
	Sn                  string `json:"sn"`
}

// GetPrintStageName converts the internal `mc_print_stage` code and `gcode_state` into a human-readable string
// representing the current activity of the printer.
//
// Example:
//
//	status := client.GetPrinterStatus()
//	fmt.Println("Printer stage:", status.GetPrintStageName())
func (p *PrinterStatus) GetPrintStageName() string {
	if p.GcodeState == "PAUSE" {
		return "Paused"
	}
	if p.GcodeState == "FINISH" {
		return "Finished"
	}
	if p.GcodeState == "IDLE" {
		return "Idle"
	}
	switch p.StgCur {
	case 0:
		return "Printing"
	case 1:
		return "Auto bed leveling"
	case 2:
		// "2" is Heatbed Preheating, but sometimes sticks during printing.
		// If we are actively running and past the first layer, just call it Printing.
		if p.GcodeState == "RUNNING" && p.LayerNum > 0 {
			return "Printing"
		}
		return "Heatbed preheating"
	case 3:
		return "Vibration compensation"
	case 4:
		return "Changing filament"
	case 5:
		return "M400 pause"
	case 6:
		return "Paused (filament ran out)"
	case 7:
		return "Heating nozzle"
	case 8:
		return "Calibrating dynamic flow"
	case 9:
		return "Scanning bed surface"
	case 10:
		return "Inspecting first layer"
	case 11:
		return "Identifying build plate type"
	case 12:
		return "Calibrating Micro Lidar"
	case 13:
		return "Homing toolhead"
	case 14:
		return "Cleaning nozzle tip"
	case 15:
		return "Checking extruder temperature"
	case 16:
		return "Paused by the user"
	case 17:
		return "Pause (front cover fall off)"
	case 18:
		return "Calibrating the micro lidar"
	case 19:
		return "Calibrating flow ratio"
	case 20:
		return "Pause (nozzle temperature malfunction)"
	case 21:
		return "Pause (heatbed temperature malfunction)"
	case 22:
		return "Filament unloading"
	case 23:
		return "Pause (step loss)"
	case 24:
		return "Filament loading"
	case 25:
		return "Motor noise cancellation"
	case 26:
		return "Pause (AMS offline)"
	case 27:
		return "Pause (low speed of the heatbreak fan)"
	case 28:
		return "Pause (chamber temperature control problem)"
	case 29:
		return "Cooling chamber"
	case 30:
		return "Pause (Gcode inserted by user)"
	case 31:
		return "Motor noise showoff"
	case 32:
		return "Pause (nozzle clumping)"
	case 33:
		return "Pause (cutter error)"
	case 34:
		return "Pause (first layer error)"
	case 35:
		return "Pause (nozzle clog)"
	case 36:
		return "Measuring motion precision"
	case 37:
		return "Enhancing motion precision"
	case 38:
		return "Measure motion accuracy"
	case 39:
		return "Nozzle offset calibration"
	case 40:
		return "High temperature auto bed leveling"
	case 41:
		return "Auto Check: Quick Release Lever"
	case 42:
		return "Auto Check: Door and Upper Cover"
	case 43:
		return "Laser Calibration"
	case 44:
		return "Auto Check: Platform"
	case 45:
		return "Confirming BirdsEye Camera location"
	case 46:
		return "Calibrating BirdsEye Camera"
	case 47:
		return "Auto bed leveling - Phase 1"
	case 48:
		return "Auto bed leveling - Phase 2"
	case 49:
		return "Heating chamber"
	case 50:
		return "Cooling heatbed"
	case 51:
		return "Printing calibration lines"
	case 52:
		return "Auto Check: Material"
	case 53:
		return "Live View Camera Calibration"
	case 54:
		return "Waiting for heatbed to reach target temperature"
	case 55:
		return "Auto Check: Material Position"
	case 56:
		return "Cutting Module Offset Calibration"
	case 57:
		return "Measuring Surface"
	case 58:
		return "Thermal Preconditioning for first layer optimization"
	case 59:
		return "Homing Blade Holder"
	case 60:
		return "Calibrating Camera Offset"
	case 61:
		return "Calibrating Blade Holder Position"
	case 62:
		return "Hotend Pick and Place Test"
	case 63:
		return "Waiting for the Chamber temperature to equalize"
	case 64:
		return "Preparing Hotend"
	case 65:
		return "Calibrating the detection position of nozzle clumping"
	case 66:
		return "Purifying the chamber air"
	case -1:
		return "Idle"
	case 255:
		return "Idle"
	default:
		return fmt.Sprintf("Unknown (%d)", p.StgCur)
	}
}

// PrintOptions configures the parameters for starting a print job.
type PrintOptions struct {
	BedType              string
	Timelapse            bool
	BedLeveling          bool
	FlowCalibration      bool
	VibrationCalibration bool
	LayerInspection      bool
	UseAMS               bool
}

// Speed Profile Constants
const (
	SpeedSilent    = "1"
	SpeedStandard  = "2"
	SpeedSport     = "3"
	SpeedLudicrous = "4"
)
