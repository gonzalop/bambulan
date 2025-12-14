package bambulan

// Message represents the top-level JSON structure received from the printer.
type Message struct {
	Print *PrinterStatus `json:"print"`
}

// PrinterStatus contains the detailed status of the printer components.
type PrinterStatus struct {
	Upload                  *Upload         `json:"upload,omitempty"`
	NozzleTemp              float64         `json:"nozzle_temper"`
	NozzleTargetTemp        float64         `json:"nozzle_target_temper"`
	BedTemp                 float64         `json:"bed_temper"`
	BedTargetTemp           float64         `json:"bed_target_temper"`
	ChamberTemp             float64         `json:"chamber_temper"`
	McPrintStage            string          `json:"mc_print_stage"`
	HeatbreakFanSpeed       string          `json:"heatbreak_fan_speed"`
	CoolingFanSpeed         string          `json:"cooling_fan_speed"`
	BigFan1Speed            string          `json:"big_fan1_speed"`
	BigFan2Speed            string          `json:"big_fan2_speed"`
	McPercent               int             `json:"mc_percent"`
	McRemainingTime         int             `json:"mc_remaining_time"`
	AmsStatus               int             `json:"ams_status"`
	AmsRfidStatus           int             `json:"ams_rfid_status"`
	HwSwitchState           int             `json:"hw_switch_state"`
	SpdMag                  int             `json:"spd_mag"`
	SpdLvl                  int             `json:"spd_lvl"`
	PrintError              int             `json:"print_error"`
	Lifecycle               string          `json:"lifecycle"`
	WifiSignal              string          `json:"wifi_signal"`
	GcodeState              string          `json:"gcode_state"`
	GcodeFilePreparePercent string          `json:"gcode_file_prepare_percent"`
	QueueNumber             int             `json:"queue_number"`
	QueueTotal              int             `json:"queue_total"`
	QueueEst                int             `json:"queue_est"`
	QueueSts                int             `json:"queue_sts"`
	ProjectId               string          `json:"project_id"`
	ProfileId               string          `json:"profile_id"`
	TaskId                  string          `json:"task_id"`
	SubtaskId               string          `json:"subtask_id"`
	SubtaskName             string          `json:"subtask_name"`
	GcodeFile               string          `json:"gcode_file"`
	Stg                     []interface{}   `json:"stg"`
	StgCur                  int             `json:"stg_cur"`
	PrintType               string          `json:"print_type"`
	HomeFlag                int             `json:"home_flag"`
	McPrintLineNumber       string          `json:"mc_print_line_number"`
	McPrintSubStage         int             `json:"mc_print_sub_stage"`
	Sdcard                  bool            `json:"sdcard"`
	ForceUpgrade            bool            `json:"force_upgrade"`
	MessProductionState     string          `json:"mess_production_state"`
	LayerNum                int             `json:"layer_num"`
	TotalLayerNum           int             `json:"total_layer_num"`
	SObj                    []interface{}   `json:"s_obj"`
	FanGear                 int             `json:"fan_gear"`
	Hms                     []interface{}   `json:"hms"`
	Online                  *Online         `json:"online,omitempty"`
	Ams                     *AMS            `json:"ams,omitempty"`
	Ipcam                   *IPCam          `json:"ipcam,omitempty"`
	VtTray                  *VTTray         `json:"vt_tray,omitempty"`
	LightsReport            []*LightsReport `json:"lights_report,omitempty"`
	UpgradeState            *UpgradeState   `json:"upgrade_state,omitempty"`
	Command                 string          `json:"command"`
	Msg                     int             `json:"msg"`
	SequenceId              string          `json:"sequence_id"`
}

type Upload struct {
	FileSize      int    `json:"file_size"`
	FinishSize    int    `json:"finish_size"`
	Status        string `json:"status"`
	Progress      int    `json:"progress"`
	Message       string `json:"message"`
	OssUrl        string `json:"oss_url"`
	SequenceId    string `json:"sequence_id"`
	Speed         int    `json:"speed"`
	TaskId        string `json:"task_id"`
	TimeRemaining int    `json:"time_remaining"`
	TroubleId     string `json:"trouble_id"`
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
	IpcamDev     string `json:"ipcam_dev"`
	IpcamRecord  string `json:"ipcam_record"`
	Timelapse    string `json:"timelapse"`
	Resolution   string `json:"resolution"`
	TutkServer   string `json:"tutk_server"`
	ModeBits     int    `json:"mode_bits"`
	RtspUrl      string `json:"rtsp_url"`
}

type LightsReport struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

type UpgradeState struct {
	SequenceId          int           `json:"sequence_id"`
	Progress            string        `json:"progress"`
	Status              string        `json:"status"`
	ConsistencyRequest  bool          `json:"consistency_request"`
	DisState            int           `json:"dis_state"`
	ErrCode             int           `json:"err_code"`
	ForceUpgrade        bool          `json:"force_upgrade"`
	Message             string        `json:"message"`
	Module              string        `json:"module"`
	NewVersionState     int           `json:"new_version_state"`
	NewVerList          []interface{} `json:"new_ver_list"`
	CurStateCode        int           `json:"cur_state_code"`
	AhbNewVersionNumber string        `json:"ahb_new_version_number"`
	AmsNewVersionNumber string        `json:"ams_new_version_number"`
	ExtNewVersionNumber string        `json:"ext_new_version_number"`
	Idx                 int           `json:"idx"`
	Idx1                int           `json:"idx1"`
	LowerLimit          string        `json:"lower_limit"`
	OtaNewVersionNumber string        `json:"ota_new_version_number"`
	Sn                  string        `json:"sn"`
}

// GetPrintStageName converts the numeric print stage code into a human-readable string.
func (p *PrinterStatus) GetPrintStageName() string {
	switch p.McPrintStage {
	case "1":
		return "Auto Bed Leveling"
	case "2":
		return "Heatbed Preheating"
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
