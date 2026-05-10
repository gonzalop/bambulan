// Package octoprint provides an OctoPrint API compatibility layer for BambuLAN.
//
// It translates between OctoPrint's JSON schema and the bambulan library's types,
// enabling slicer software (OrcaSlicer, PrusaSlicer, etc.) to communicate with
// Bambu Lab printers as if they were OctoPrint-managed devices.
//
// Usage:
//
//	adapter := octoprint.NewAdapter(client)
//
//	// Get printer state as OctoPrint JSON
//	state, err := adapter.GetPrinterState()
//
//	// Handle a job command from a POST body
//	var cmd octoprint.JobCommand
//	json.Unmarshal(body, &cmd)
//	err := adapter.ExecuteJobCommand(cmd)
package octoprint

// VersionResponse is the response for GET /api/version.
type VersionResponse struct {
	API    string `json:"api"`
	Server string `json:"server"`
	Text   string `json:"text"`
}

// ConnectionState describes the current printer connection.
type ConnectionState struct {
	State          string `json:"state"`
	Port           string `json:"port"`
	Baudrate       int    `json:"baudrate"`
	PrinterProfile string `json:"printerProfile"`
}

// ConnectionResponse is the response for GET /api/connection.
type ConnectionResponse struct {
	Current ConnectionState `json:"current"`
}

// TemperatureReading is a single temperature sensor reading.
type TemperatureReading struct {
	Actual float64 `json:"actual"`
	Target float64 `json:"target"`
}

// PrinterStateFlags represents the boolean state flags of the printer.
type PrinterStateFlags struct {
	Operational   bool `json:"operational"`
	Printing      bool `json:"printing"`
	Paused        bool `json:"paused"`
	Error         bool `json:"error"`
	Ready         bool `json:"ready"`
	ClosedOrError bool `json:"closedOrError"`
}

// PrinterState is the state block in a printer response.
type PrinterState struct {
	Text  string            `json:"text"`
	Flags PrinterStateFlags `json:"flags"`
}

// PrinterResponse is the response for GET /api/printer.
type PrinterResponse struct {
	Temperature map[string]TemperatureReading `json:"temperature"`
	State       PrinterState                  `json:"state"`
}

// JobFile describes the file being printed.
type JobFile struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

// JobInfo is the job block within a job response.
type JobInfo struct {
	File               JobFile `json:"file"`
	EstimatedPrintTime *int    `json:"estimatedPrintTime"`
}

// JobProgress is the progress block within a job response.
type JobProgress struct {
	Completion    float64 `json:"completion"`
	PrintTimeLeft int     `json:"printTimeLeft"`
}

// JobResponse is the response for GET /api/job.
type JobResponse struct {
	Job      JobInfo     `json:"job"`
	Progress JobProgress `json:"progress"`
	State    string      `json:"state"`
}

// FileUploadResponse is the response for POST /api/files/local.
type FileUploadResponse struct {
	Files map[string]FileInfo `json:"files"`
	Done  bool                `json:"done"`
}

// FileInfo describes an uploaded file (used in upload responses).
type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FileEntry describes a single file or folder in an OctoPrint file listing.
// Folders carry a Children slice; files carry size and type information.
type FileEntry struct {
	// Name is the base name of the file or folder.
	Name string `json:"name"`
	// Path is the full path relative to the storage root (no leading slash).
	Path string `json:"path"`
	// Type is "machinecode" for printable files or "folder" for directories.
	Type string `json:"type"`
	// TypePath mirrors Type as a slice (e.g. ["machinecode","gcode"] or ["folder"]).
	TypePath []string `json:"typePath"`
	// Origin is always "local" for SD card files.
	Origin string `json:"origin,omitempty"`
	// Size is the file size in bytes (omitted for folders).
	Size int64 `json:"size,omitempty"`
	// Children contains nested entries for folders (omitted for files).
	Children []FileEntry `json:"children,omitempty"`
	// Refs contains self-referential URLs for resource and download links.
	Refs *FileRefs `json:"refs,omitempty"`
}

// FileRefs holds the URL references embedded in each FileEntry.
type FileRefs struct {
	Resource string `json:"resource"`
	Download string `json:"download,omitempty"`
}

// FilesResponse is the response for GET /api/files and GET /api/files/local.
type FilesResponse struct {
	Files []FileEntry `json:"files"`
}

// JobCommand is the body for POST /api/job.
type JobCommand struct {
	// Command is the top-level action: "start", "cancel", "restart", "pause".
	Command string `json:"command"`
	// Action is used with "pause" to specify "pause", "resume", or "toggle".
	Action string `json:"action"`
}

// PrinterCommand is the body for POST /api/printer/command.
type PrinterCommand struct {
	// Command is a single G-code line.
	Command string `json:"command"`
	// Commands is a slice of G-code lines (alternative to Command).
	Commands []string `json:"commands"`
}

// PrintheadCommand is the body for POST /api/printer/printhead.
type PrintheadCommand struct {
	// Command is "home" or "jog".
	Command string `json:"command"`
	// Axes is a list of axes to home (e.g. ["x", "y"]).
	Axes []string `json:"axes"`
	// X, Y, Z are relative or absolute move distances for jogging.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
	// Absolute specifies whether the move is absolute or relative.
	Absolute bool `json:"absolute"`
}

// BedCommand is the body for POST /api/printer/bed.
type BedCommand struct {
	// Command is "target" or "offset".
	Command string `json:"command"`
	// Target is the target temperature.
	Target int `json:"target"`
	// Offset is the temperature offset (not commonly used with Bambu).
	Offset int `json:"offset"`
}

// ChamberCommand is the body for POST /api/printer/chamber.
type ChamberCommand struct {
	// Command is "target" or "offset".
	Command string `json:"command"`
	// Target is the target temperature.
	Target int `json:"target"`
	// Offset is the temperature offset.
	Offset int `json:"offset"`
}

// ToolCommand is the body for POST /api/printer/tool.
type ToolCommand struct {
	// Command is "target", "offset", or "select".
	Command string `json:"command"`
	// Targets is a map of tool IDs to target temperatures.
	Targets map[string]int `json:"targets"`
	// Offsets is a map of tool IDs to temperature offsets.
	Offsets map[string]int `json:"offsets"`
	// Tool is the ID of the tool to select (e.g. "tool0").
	Tool string `json:"tool"`
	// Amount is the amount to extrude or retract (in mm).
	Amount int `json:"amount"`
}

// PrinterProfileResponse is the response for GET /api/printerprofiles.
type PrinterProfileResponse struct {
	Profiles map[string]PrinterProfile `json:"profiles"`
}

// PrinterProfile defines machine dimensions and extruder configuration.
type PrinterProfile struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Model         string                `json:"model"`
	Default       bool                  `json:"default"`
	Current       bool                  `json:"current"`
	Volume        PrinterVolume         `json:"volume"`
	Extruder      PrinterExtruderConfig `json:"extruder"`
	HeatedBed     bool                  `json:"heatedBed"`
	HeatedChamber bool                  `json:"heatedChamber"`
}

// PrinterVolume defines the build area.
type PrinterVolume struct {
	FormFactor string `json:"formFactor"` // "rectangular" or "circular"
	Width      int    `json:"width"`
	Depth      int    `json:"depth"`
	Height     int    `json:"height"`
	Origin     string `json:"origin"` // "lowerleft" or "center"
}

// PrinterExtruderConfig defines the extruder setup.
type PrinterExtruderConfig struct {
	Count          int     `json:"count"`
	NozzleDiameter float64 `json:"nozzleDiameter"`
	SharedNozzle   bool    `json:"sharedNozzle"`
}

// TimelapseResponse is the response for GET /api/timelapse.
type TimelapseResponse struct {
	Files   []TimelapseFile `json:"files"`
	Enabled bool            `json:"enabled"`
}

// TimelapseFile describes a single recorded timelapse video.
type TimelapseFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	Date string `json:"date"` // Format: "YYYY-MM-DD HH:MM"
}
