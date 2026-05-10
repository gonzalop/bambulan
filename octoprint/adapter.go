package octoprint

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/gonzalop/bambulan"
)

// Adapter translates between the OctoPrint API schema and the bambulan library.
// It is stateless with respect to HTTP — it accepts bambulan types as input and
// returns OctoPrint-schema types as output, with no net/http dependency.
type Adapter struct {
	client *bambulan.Client
}

// NewAdapter creates a new OctoPrint adapter backed by the given bambulan Client.
func NewAdapter(client *bambulan.Client) *Adapter {
	return &Adapter{client: client}
}

// Version returns the static OctoPrint version response used for slicer handshake.
func (a *Adapter) Version() VersionResponse {
	return VersionResponse{
		API:    "0.1",
		Server: "1.8.6",
		Text:   "OctoPrint 1.8.6 (BambuLAN Emulation)",
	}
}

// Connection returns the static OctoPrint connection response.
// The printer is always considered "Operational" when an adapter exists.
func (a *Adapter) Connection() ConnectionResponse {
	return ConnectionResponse{
		Current: ConnectionState{
			State:          "Operational",
			Port:           "/dev/bambulan",
			Baudrate:       115200,
			PrinterProfile: "_default",
		},
	}
}

// PrinterState maps the current PrinterStatus to an OctoPrint printer response.
// Returns an error if the status is nil.
func (a *Adapter) PrinterState(ctx context.Context, st *bambulan.PrinterStatus) (PrinterResponse, error) {
	if st == nil {
		return PrinterResponse{}, fmt.Errorf("printer status not available")
	}

	caps := bambulan.GetPrinterCapabilities(st.DeviceModel)

	temps := map[string]TemperatureReading{
		"bed": {
			Actual: st.BedTemp,
			Target: st.BedTargetTemp,
		},
		"tool0": {
			Actual: st.NozzleTemp,
			Target: st.NozzleTargetTemp,
		},
	}

	// Additional extruders (placeholder values; PrinterStatus only carries one nozzle temp today)
	for i := 1; i < caps.NumExtruders; i++ {
		temps[fmt.Sprintf("tool%d", i)] = TemperatureReading{}
	}

	// Chamber temperature (include when supported or noticeably warm)
	if caps.HasChamberHeater || st.ChamberTemp > 5 {
		temps["chamber"] = TemperatureReading{
			Actual: st.ChamberTemp,
			Target: st.ChamberTargetTemp,
		}
	}

	return PrinterResponse{
		Temperature: temps,
		State: PrinterState{
			Text: st.GetPrintStageName(),
			Flags: PrinterStateFlags{
				Operational:   true,
				Printing:      st.GcodeState == "RUNNING",
				Paused:        st.GcodeState == "PAUSE",
				Error:         st.PrintError != 0,
				Ready:         st.GcodeState == "IDLE",
				ClosedOrError: false,
			},
		},
	}, nil
}

// JobState maps the current PrinterStatus to an OctoPrint job response.
// Returns an error if the status is nil.
func (a *Adapter) JobState(ctx context.Context, st *bambulan.PrinterStatus) (JobResponse, error) {
	if st == nil {
		return JobResponse{}, fmt.Errorf("printer status not available")
	}

	stateStr := "Operational"
	switch {
	case st.Lifecycle == "printing" || st.GcodeState == "RUNNING":
		stateStr = "Printing"
	case st.GcodeState == "PAUSE":
		stateStr = "Paused"
	}

	return JobResponse{
		Job: JobInfo{
			File: JobFile{
				Name:   st.SubtaskName,
				Origin: "local",
			},
		},
		Progress: JobProgress{
			Completion:    float64(st.McPercent),
			PrintTimeLeft: st.McRemainingTime * 60,
		},
		State: stateStr,
	}, nil
}

// ExecuteJobCommand dispatches an OctoPrint job command to the printer.
// It uses the printer's current status to validate state transitions.
func (a *Adapter) ExecuteJobCommand(ctx context.Context, cmd JobCommand, st *bambulan.PrinterStatus) error {
	if st == nil {
		return fmt.Errorf("printer status not available")
	}

	isActive := st.Lifecycle == "printing" || st.GcodeState == "RUNNING" || st.GcodeState == "PAUSE"
	isPaused := st.GcodeState == "PAUSE"

	switch cmd.Command {
	case "start":
		if isActive {
			return &ConflictError{Message: "print already active"}
		}
		return &ConflictError{Message: "starting without file selection is not supported"}

	case "cancel":
		if !isActive {
			return &ConflictError{Message: "no active print"}
		}
		_, err := a.client.MQTT.StopPrint(ctx)
		return err

	case "restart":
		if !isActive || !isPaused {
			return &ConflictError{Message: "print must be active and paused to restart"}
		}
		return &ConflictError{Message: "restart is not supported"}

	case "pause":
		if !isActive {
			return &ConflictError{Message: "no active print to pause"}
		}

		action := cmd.Action
		if action == "" || action == "toggle" {
			if isPaused {
				action = "resume"
			} else {
				action = "pause"
			}
		}

		switch action {
		case "pause":
			if isPaused {
				return nil // Already paused — no-op
			}
			_, err := a.client.MQTT.PausePrint(ctx)
			return err
		case "resume":
			if !isPaused {
				return nil // Already running — no-op
			}
			_, err := a.client.MQTT.ResumePrint(ctx)
			return err
		default:
			return fmt.Errorf("invalid pause action: %q", action)
		}

	default:
		return fmt.Errorf("unknown job command: %q", cmd.Command)
	}
}

// ExecutePrinterCommand sends one or more G-code lines to the printer.
func (a *Adapter) ExecutePrinterCommand(ctx context.Context, cmd PrinterCommand) error {
	lines := cmd.Commands
	if cmd.Command != "" {
		lines = append(lines, cmd.Command)
	}
	if len(lines) == 0 {
		return nil
	}
	gcode := strings.Join(lines, "\n") + "\n"
	_, err := a.client.MQTT.SendGCode(ctx, gcode)
	return err
}

// ExecutePrintheadCommand handles axis homing and jogging.
func (a *Adapter) ExecutePrintheadCommand(ctx context.Context, cmd PrintheadCommand) error {
	switch cmd.Command {
	case "home":
		gcode := "G28"
		for _, axis := range cmd.Axes {
			gcode += " " + strings.ToUpper(axis)
		}
		_, err := a.client.MQTT.SendGCode(ctx, gcode+"\n")
		return err

	case "jog":
		var lines []string
		if cmd.Absolute {
			lines = append(lines, "G90")
		} else {
			lines = append(lines, "G91")
		}

		move := "G1"
		if cmd.X != 0 {
			move += fmt.Sprintf(" X%.3f", cmd.X)
		}
		if cmd.Y != 0 {
			move += fmt.Sprintf(" Y%.3f", cmd.Y)
		}
		if cmd.Z != 0 {
			move += fmt.Sprintf(" Z%.3f", cmd.Z)
		}
		move += " F3000" // Default travel speed
		lines = append(lines, move)

		// Always revert to absolute positioning
		lines = append(lines, "G90")

		_, err := a.client.MQTT.SendGCode(ctx, strings.Join(lines, "\n")+"\n")
		return err

	default:
		return fmt.Errorf("unknown printhead command: %q", cmd.Command)
	}
}

// ExecuteBedCommand sets the target bed temperature.
func (a *Adapter) ExecuteBedCommand(ctx context.Context, cmd BedCommand) error {
	if cmd.Command != "target" {
		return fmt.Errorf("unsupported bed command: %q", cmd.Command)
	}
	_, err := a.client.MQTT.SetBedTemperature(ctx, cmd.Target)
	return err
}

// ExecuteChamberCommand sets the target chamber temperature.
func (a *Adapter) ExecuteChamberCommand(ctx context.Context, cmd ChamberCommand) error {
	if cmd.Command != "target" {
		return fmt.Errorf("unsupported chamber command: %q", cmd.Command)
	}
	_, err := a.client.MQTT.SetChamberTemperature(ctx, cmd.Target)
	return err
}

// ExecuteToolCommand handles nozzle temperature and extrusion.
func (a *Adapter) ExecuteToolCommand(ctx context.Context, cmd ToolCommand) error {
	switch cmd.Command {
	case "target":
		// tool0 is always the primary (and currently only) tool reported
		if temp, ok := cmd.Targets["tool0"]; ok {
			_, err := a.client.MQTT.SetNozzleTemperature(ctx, temp, 0)
			return err
		}
		return nil

	case "extrude", "retract":
		amount := float64(cmd.Amount)
		if cmd.Command == "retract" {
			amount = -amount
		}
		if amount == 0 {
			return nil
		}
		// Use relative E moves
		gcode := fmt.Sprintf("M83\nG1 E%.3f F300\n", amount)
		_, err := a.client.MQTT.SendGCode(ctx, gcode)
		return err

	case "select":
		// Map tool selection (tool0, tool1, etc.) to AMS slots?
		// For now, we only acknowledge tool0 which is the active head.
		return nil

	default:
		return fmt.Errorf("unknown tool command: %q", cmd.Command)
	}
}

// UploadAndPrint uploads a file to the printer and optionally starts a print.
// reader is the file data stream, filename is the base name, remotePath is the
// full destination path on the printer's SD card.
//
// If print is true, a print job is started with sensible defaults after upload.
func (a *Adapter) UploadAndPrint(ctx context.Context, reader io.Reader, remotePath, filename string, print bool) (FileUploadResponse, error) {
	if err := a.client.File.Upload(ctx, remotePath, reader, nil); err != nil {
		return FileUploadResponse{}, fmt.Errorf("upload failed: %w", err)
	}

	if print {
		opts := bambulan.PrintOptions{
			BedType:              "auto",
			BedLeveling:          true,
			FlowCalibration:      false,
			VibrationCalibration: true,
		}
		if _, err := a.client.MQTT.StartPrint(ctx, filename, opts); err != nil {
			// Don't fail the whole request if upload succeeded but print failed.
			// The caller should log this.
			_ = err
		}
	}

	return FileUploadResponse{
		Files: map[string]FileInfo{
			"local": {
				Name: filename,
				Path: filename,
			},
		},
		Done: true,
	}, nil
}

// ConflictError is returned when a state-transition command is invalid given
// the printer's current state (e.g. cancelling when nothing is printing).
// HTTP handlers should map this to 409 Conflict.
type ConflictError struct {
	Message string
}

func (e *ConflictError) Error() string {
	return e.Message
}

// ListFiles returns an OctoPrint-schema file tree for the given directory.
//
// When recursive is true, subdirectories are expanded by making additional FTP
// LIST calls. baseURL is the scheme+host of the server (e.g.
// "http://192.168.1.5:8080") used to build the refs.resource and refs.download
// URLs embedded in each entry.
//
// Returns a FilesResponse whose Files slice mirrors the OctoPrint folder-tree
// schema, where folders embed a Children slice of their contents.
func (a *Adapter) ListFiles(ctx context.Context, dir string, recursive bool, baseURL string) (FilesResponse, error) {
	entries, err := a.buildTree(ctx, dir, dir, recursive, baseURL)
	if err != nil {
		return FilesResponse{}, err
	}
	return FilesResponse{Files: entries}, nil
}

// buildTree recursively fetches FTP entries under ftpDir and maps them into
// OctoPrint FileEntry values. rootDir is the top-level directory of this
// listing call, used to construct relative paths.
func (a *Adapter) buildTree(ctx context.Context, ftpDir, rootDir string, recursive bool, baseURL string) ([]FileEntry, error) {
	rawEntries, err := a.client.File.ListFiles(ctx, ftpDir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", ftpDir, err)
	}

	var result []FileEntry
	for _, e := range rawEntries {
		if e.Name == "." || e.Name == ".." {
			continue
		}

		// Build the full FTP path and the relative path for OctoPrint
		fullPath := path.Join(ftpDir, e.Name)
		// relPath is relative to rootDir, with no leading slash
		relPath := strings.TrimPrefix(fullPath, strings.TrimSuffix(rootDir, "/")+"/")
		relPath = strings.TrimPrefix(relPath, "/")

		switch e.Type {
		case "dir":
			fe := FileEntry{
				Name:     e.Name,
				Path:     relPath,
				Type:     "folder",
				TypePath: []string{"folder"},
			}
			if recursive {
				children, err := a.buildTree(ctx, fullPath, rootDir, recursive, baseURL)
				if err != nil {
					return nil, err
				}
				fe.Children = children
			}
			result = append(result, fe)

		default: // "file" or "link"
			fe := FileEntry{
				Name:     e.Name,
				Path:     relPath,
				Origin:   "local",
				Size:     e.Size,
				Type:     fileType(e.Name),
				TypePath: fileTypePath(e.Name),
				Refs: &FileRefs{
					Resource: baseURL + "/api/files/local/" + relPath,
					Download: baseURL + "/api/files/local/" + relPath + "?download=true",
				},
			}
			result = append(result, fe)
		}
	}
	return result, nil
}

// fileType returns the OctoPrint type string for a filename.
// Printable files (gcode, 3mf) are "machinecode"; everything else is "model".
func fileType(name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".gcode") ||
		strings.HasSuffix(lower, ".gcode.3mf") ||
		strings.HasSuffix(lower, ".3mf") ||
		strings.HasSuffix(lower, ".gco") {
		return "machinecode"
	}
	return "model"
}

// fileTypePath returns the OctoPrint typePath slice for a filename.
func fileTypePath(name string) []string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".gcode") || strings.HasSuffix(lower, ".gco"):
		return []string{"machinecode", "gcode"}
	case strings.HasSuffix(lower, ".gcode.3mf") || strings.HasSuffix(lower, ".3mf"):
		return []string{"machinecode", "gcode"}
	default:
		return []string{"model"}
	}
}

// DeleteFile deletes a file from the printer's SD card.
// remotePath must be an absolute path (e.g. "/model.gcode.3mf").
func (a *Adapter) DeleteFile(ctx context.Context, remotePath string) error {
	return a.client.File.Delete(ctx, remotePath)
}
