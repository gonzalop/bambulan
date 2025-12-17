package bambulan

// bambuMessage represents the top-level JSON structure received from the printer.
// It is used internally to unwrap the "print" object.
type bambuMessage struct {
	Print *PrinterStatus `json:"print"`
}

// PrinterStatus contains the detailed status of the printer components.
type PrinterStatus struct {
	Upload                  *Upload         `json:"upload,omitempty"`
	NozzleTemp              float64         `json:"nozzle_temper"`        // Actual nozzle temperature in Celsius.
	NozzleTargetTemp        float64         `json:"nozzle_target_temper"` // Target nozzle temperature in Celsius.
	BedTemp                 float64         `json:"bed_temper"`           // Actual bed temperature in Celsius.
	BedTargetTemp           float64         `json:"bed_target_temper"`    // Target bed temperature in Celsius.
	ChamberTemp             float64         `json:"chamber_temper"`       // Actual chamber temperature in Celsius.
	McPrintStage            string          `json:"mc_print_stage"`       // Internal code for the current mechanical print stage (see GetPrintStageName).
	HeatbreakFanSpeed       string          `json:"heatbreak_fan_speed"`  // Speed of the heatbreak fan.
	CoolingFanSpeed         string          `json:"cooling_fan_speed"`    // Speed of the part cooling fan.
	BigFan1Speed            string          `json:"big_fan1_speed"`       // Speed of the auxiliary fan.
	BigFan2Speed            string          `json:"big_fan2_speed"`       // Speed of the chamber fan.
	McPercent               int             `json:"mc_percent"`           // Print progress percentage (0-100).
	McRemainingTime         int             `json:"mc_remaining_time"`    // Estimated remaining print time in minutes.
	AMSStatus               int             `json:"ams_status"`           // AMS status code.
	AMSRFIDStatus           int             `json:"ams_rfid_status"`      // AMS RFID status code.
	HwSwitchState           int             `json:"hw_switch_state"`      // Hardware switch state.
	SpdMag                  int             `json:"spd_mag"`              // Speed multiplier magnitude (e.g., 50, 100, 125, 166).
	SpdLvl                  int             `json:"spd_lvl"`              // Current speed profile level (1=Silent, 2=Standard, 3=Sport, 4=Ludicrous).
	PrintError              int             `json:"print_error"`          // Error code if a print error occurred.
	Lifecycle               string          `json:"lifecycle"`            // Printer lifecycle state (e.g., "printing", "idle").
	WifiSignal              string          `json:"wifi_signal"`          // WiFi signal strength.
	GcodeState              string          `json:"gcode_state"`          // Current G-code execution state (e.g., "RUNNING", "PAUSE", "IDLE", "FINISH").
	GcodeFilePreparePercent string          `json:"gcode_file_prepare_percent"`
	QueueNumber             int             `json:"queue_number"`
	QueueTotal              int             `json:"queue_total"`
	QueueEst                int             `json:"queue_est"`
	QueueSts                int             `json:"queue_sts"`
	ProjectID               string          `json:"project_id"`
	ProfileID               string          `json:"profile_id"`
	TaskID                  string          `json:"task_id"`
	SubtaskID               string          `json:"subtask_id"`
	SubtaskName             string          `json:"subtask_name"`
	GcodeFile               string          `json:"gcode_file"`
	Stg                     []any           `json:"stg"`
	StgCur                  int             `json:"stg_cur"`
	PrintType               string          `json:"print_type"`
	HomeFlag                int             `json:"home_flag"`
	McPrintLineNumber       string          `json:"mc_print_line_number"`
	McPrintSubStage         int             `json:"mc_print_sub_stage"`
	Sdcard                  bool            `json:"sdcard"`
	ForceUpgrade            bool            `json:"force_upgrade"`
	MessProductionState     string          `json:"mess_production_state"`
	LayerNum                int             `json:"layer_num"`       // Current layer number.
	TotalLayerNum           int             `json:"total_layer_num"` // Total number of layers.
	SObj                    []any           `json:"s_obj"`
	FanGear                 int             `json:"fan_gear"`
	Hms                     []any           `json:"hms"`
	Online                  *Online         `json:"online,omitempty"`
	Ams                     *AMS            `json:"ams,omitempty"`
	IPCam                   *IPCam          `json:"ipcam,omitempty"`
	VtTray                  *VTTray         `json:"vt_tray,omitempty"`
	LightsReport            []*LightsReport `json:"lights_report,omitempty"`
	UpgradeState            *UpgradeState   `json:"upgrade_state,omitempty"`
	Command                 string          `json:"command"`
	Msg                     int             `json:"msg"`
	SequenceID              string          `json:"sequence_id"` // Sequence ID of the last command, for correlating responses.
	Result                  string          `json:"result"`      // Result of the last command (e.g., "success").
	Reason                  string          `json:"reason"`      // Reason for command failure, if any.
}

type Upload struct {
	FileSize      int    `json:"file_size"`
	FinishSize    int    `json:"finish_size"`
	Status        string `json:"status"`
	Progress      int    `json:"progress"`
	Message       string `json:"message"`
	OSSURL        string `json:"oss_url"`
	SequenceID    string `json:"sequence_id"`
	Speed         int    `json:"speed"`
	TaskID        string `json:"task_id"`
	TimeRemaining int    `json:"time_remaining"`
	TroubleID     string `json:"trouble_id"`
}

type Online struct {
	Ahb     bool `json:"ahb"`
	Ext     bool `json:"ext"`
	Rfid    bool `json:"rfid"`
	Version int  `json:"version"`
}

type VTTray struct {
	Id            string   `json:"id"`
	TagUid        string   `json:"tag_uid"`
	TrayIdName    string   `json:"tray_id_name"`
	TrayInfoIdx   string   `json:"tray_info_idx"`
	TrayType      string   `json:"tray_type"`
	TraySubBrands string   `json:"tray_sub_brands"`
	TrayColor     string   `json:"tray_color"`
	TrayWeight    string   `json:"tray_weight"`
	TrayDiameter  string   `json:"tray_diameter"`
	TrayTemp      string   `json:"tray_temp"`
	TrayTime      string   `json:"tray_time"`
	BedTempType   string   `json:"bed_temp_type"`
	BedTemp       string   `json:"bed_temp"`
	NozzleTempMax string   `json:"nozzle_temp_max"`
	NozzleTempMin string   `json:"nozzle_temp_min"`
	XcamInfo      string   `json:"xcam_info"`
	TrayUuid      string   `json:"tray_uuid"`
	Remain        int      `json:"remain"`
	K             float64  `json:"k"`
	N             int      `json:"n"`
	CaliIdx       int      `json:"cali_idx"`
	Cols          []string `json:"cols"`
	Ctype         int      `json:"ctype"`
	DryingTemp    string   `json:"drying_temp"`
	DryingTime    string   `json:"drying_time"`
}

type AMSEntry struct {
	Humidity string    `json:"humidity"`
	Id       string    `json:"id"`
	Temp     string    `json:"temp"`
	Tray     []*VTTray `json:"tray"`
}

type AMS struct {
	Ams              []*AMSEntry `json:"ams"`
	AmsExistBits     string      `json:"ams_exist_bits"`
	AmsExistBitsRaw  string      `json:"ams_exist_bits_raw"`
	TrayExistBits    string      `json:"tray_exist_bits"`
	TrayIsBblBits    string      `json:"tray_is_bbl_bits"`
	TrayTar          string      `json:"tray_tar"`
	TrayNow          string      `json:"tray_now"`
	TrayPre          string      `json:"tray_pre"`
	TrayReadDoneBits string      `json:"tray_read_done_bits"`
	TrayReadingBits  string      `json:"tray_reading_bits"`
	Version          int         `json:"version"`
	InsertFlag       bool        `json:"insert_flag"`
	PowerOnFlag      bool        `json:"power_on_flag"`
}

type IPCam struct {
	AgoraService string `json:"agora_service"`
	IPCamDev     string `json:"ipcam_dev"`
	IPCamRecord  string `json:"ipcam_record"`
	Timelapse    string `json:"timelapse"`
	Resolution   string `json:"resolution"`
	TutkServer   string `json:"tutk_server"`
	ModeBits     int    `json:"mode_bits"`
	RTSPURL      string `json:"rtsp_url"`
}

type LightsReport struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

type UpgradeState struct {
	SequenceID          int    `json:"sequence_id"`
	Progress            string `json:"progress"`
	Status              string `json:"status"`
	ConsistencyRequest  bool   `json:"consistency_request"`
	DisState            int    `json:"dis_state"`
	ErrCode             int    `json:"err_code"`
	ForceUpgrade        bool   `json:"force_upgrade"`
	Message             string `json:"message"`
	Module              string `json:"module"`
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
	// If explicitly paused, return "Paused" regardless of the mechanical stage
	if p.GcodeState == "PAUSE" {
		return "Paused"
	}
	if p.GcodeState == "FINISH" {
		return "Finished"
	}
	switch p.McPrintStage {
	case "1":
		// Stage 1 is "Auto Bed Leveling", but also seems to be a default/idle state
		// when not printing. Check GcodeState to disambiguate.
		if p.GcodeState == "RUNNING" || p.GcodeState == "PREPARE" {
			return "Auto Bed Leveling"
		}
		// If not running, return the Gcode state (Idle, Failed, Finish, etc.)
		// Capitalize first letter if possible, or just return raw.
		if p.GcodeState == "IDLE" {
			return "Idle"
		}
		// Return state as is (e.g. FAILED, FINISH) if we aren't leveling
		return p.GcodeState
	case "2":
		return "Printing"
	case "3":
		return "Sweeping XY Mech Mode"
	case "4":
		return "Changing Filament"
	case "5":
		return "M400 Pause"
	case "6":
		return "Paused"
	case "7":
		return "Heating Hotend"
	case "8":
		return "Calibrating Extrusion"
	case "9":
		return "Printing"
	case "10":
		return "Auto Bed Leveling"
	case "13":
		return "Homing Toolhead"
	case "14":
		return "Cleaning Nozzle Tip"
	case "15":
		return "Checking Extruder"
	case "17":
		return "Printing"
	case "23":
		return "AMS Calibration"
	case "30":
		return "Paused"
	case "":
		return "Idle"
	default:
		return "Unknown"
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
