package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/pkg/filament"
)

var version = "dev"

var cli struct {
	Version kong.VersionFlag `short:"v" help:"Print version"`
	Host    string           `help:"Printer IP or hostname" env:"BAMBULAN_HOST" required:"" short:"H"`
	Code    string           `help:"Access code" env:"BAMBULAN_CODE" required:"" short:"c"`
	Serial  string           `help:"Printer serial number" env:"BAMBULAN_SERIAL" required:"" short:"s"`
	Level   string           `help:"Log level" default:"info" enum:"debug,info,warn,error" name:"log-level" short:"l"`

	Status       StatusCmd       `cmd:"" help:"Monitor printer status"`
	ChamberLight ChamberLightCmd `cmd:"" help:"Turn chamber light on or off"`
	Speed        SpeedCmd        `cmd:"" help:"Set speed: silent, standard, sport, ludicrous"`
	Print        PrintCmd        `cmd:"" help:"Control print: start, pause, resume, stop, skip objects"`
	SendGCode    SendGCodeCmd    `cmd:"" help:"Send raw G-Code command (single line only)"`
	Ams          AmsCmd          `cmd:"" help:"AMS controls"`
	Config       ConfigCmd       `cmd:"" help:"Printer configuration (options, accessories)"`
	Temp         TempCmd         `cmd:"" help:"Control temperature (head, bed)"`
	Fan          FanCmd          `cmd:"" help:"Control fan speed"`
	File         FileCmd         `cmd:"" help:"File management (ls, download, rm, mkdir, mv)"`
	Capture      CaptureCmd      `cmd:"" help:"Capture camera frame"`
	DumpInfo     DumpInfoCmd     `cmd:"" help:"Dump full printer status as JSON"`
	Web          WebCmd          `cmd:"" help:"Start web interface"`
}

type Context struct {
	Client *bambulan.Client
}

// Commands

type StatusCmd struct {
	Watch bool `help:"Watch for updates" short:"w"`
}

func (c *StatusCmd) Run(ctx *Context) error {
	client := ctx.Client
	// Channel to signal that we received at least one update
	updateReceived := make(chan struct{}, 1)

	// For status, we update the callback to print
	client.MQTT.OnUpdate = func(status *bambulan.PrinterStatus) {
		if c.Watch {
			c.printStatus(status)
		}

		select {
		case updateReceived <- struct{}{}:
		default:
		}
	}

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()

	// If watching, handle interrupts.
	if c.Watch {
		// Capture interrupt signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		fmt.Println("\nExiting...")
		return nil
	}

	// Not watching: wait for "complete" update or timeout
	// "Complete" means we have Print status (e.g. McPrintStage) AND Modules info (from get_version)
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			status := client.GetPrinterStatus() // Need to expose this or use ctx.Client.MQTT.GetPrinterStatus()
			hasPrint := status.McPrintStage != "" || status.GcodeState != ""
			hasInfo := len(status.Modules) > 0 || status.DeviceModel != ""

			if hasPrint && hasInfo {
				c.printStatus(status)
				return nil
			}
		case <-timeout:
			// Timeout, print what we have
			c.printStatus(client.MQTT.GetPrinterStatus())
			return nil
		}
	}
}

func (c *StatusCmd) printStatus(status *bambulan.PrinterStatus) {
	if c.Watch {
		fmt.Printf("\033[2J\033[H") // Clear screen only in watch mode
	}
	fmt.Println("=== Bambu Printer Status ===")
	fmt.Printf("Device Model: %s\n", status.DeviceModel)
	fmt.Println("----------------------------")
	fmt.Printf("Stage:        %s (%s)\n", status.McPrintStage, status.GetPrintStageName())
	fmt.Printf("Progress:     %d%%\n", status.McPercent)
	fmt.Printf("Remaining:    %d min\n", status.McRemainingTime)
	fmt.Printf("Layer:        %d / %d\n", status.LayerNum, status.TotalLayerNum)
	fmt.Printf("Nozzle Temp:  %.1f / %.1f °C (Limit: %d°C)\n", status.NozzleTemp, status.NozzleTargetTemp, status.NozzleTempLimit)
	fmt.Printf("Bed Temp:     %.1f / %.1f °C (Limit: %d°C)\n", status.BedTemp, status.BedTargetTemp, status.BedTempLimit)
	fmt.Printf("Chamber Temp: %.1f °C\n", status.ChamberTemp)
	fmt.Printf("Fan - Part:   %s\n", formatFan(status.CoolingFanSpeed))
	fmt.Printf("Fan - Aux:    %s\n", formatFan(status.BigFan1Speed))
	fmt.Printf("Fan - Chamb:  %s\n", formatFan(status.BigFan2Speed))
	fmt.Printf("Speed Lvl:    %d\n", status.SpdLvl)
	if len(status.LightsReport) > 0 {
		fmt.Print("Light:        ")
		for i, l := range status.LightsReport {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s:%s", l.Node, l.Mode)
		}
		fmt.Println()
	} else {
		fmt.Println("Light:        N/A")
	}

	if status.Ams != nil {
		fmt.Println("\n--- AMS Status ---")
		if len(status.Ams.Ams) == 0 {
			fmt.Println("AMS detected but no units reported.")
		}
		// TrayNow is the global slot ID (0-15 across 4 units)
		activeTray := status.Ams.TrayNow

		for i, unit := range status.Ams.Ams {
			hum := unit.Humidity
			if hum == "1" {
				hum = "1 (Dry)"
			} else if hum == "5" {
				hum = "5 (Wet)"
			}
			fmt.Printf("Unit %d: Temp=%s, Humidity=%s\n", i, unit.Temp, hum)
			for j, tray := range unit.Tray {
				// Calculate global ID for this slot
				globalID := fmt.Sprintf("%d", (i*4)+j)
				isActive := (globalID == activeTray)

				marker := " "
				if isActive {
					marker = "*"
				}

				if tray.Id == "" {
					fmt.Printf(" %sSlot %d: [Empty]\n", marker, j)
					continue
				}
				remain := fmt.Sprintf("%d%%", tray.Remain)
				if tray.Remain < 0 {
					remain = "Capacity: N/A"
				}
				name := tray.TraySubBrands
				if name == "" {
					name = tray.TrayType
				}
				if name == "" {
					name = tray.TrayIdName
				}
				fmt.Printf(" %sSlot %d: %s %s (%s)\n", marker, j, name, tray.TrayColor, remain)
			}
		}
	}

	// Module Information
	if len(status.Modules) > 0 {
		fmt.Println("\n--- Module Information ---")
		for _, m := range status.Modules {
			fmt.Printf("  %-10s %-15s HW:%-8s SW:%s\n", m.Name, m.Project, m.HwVer, m.SwVer)
		}
	}
	fmt.Println("----------------------------")
	if c.Watch {
		fmt.Println("Press Ctrl+C to exit")
	}
}

type ChamberLightCmd struct {
	State string `arg:"" enum:"on,off" help:"State: on, off"`
}

func (c *ChamberLightCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	state := c.State == "on"
	if _, err := client.MQTT.SetChamberLight(state); err != nil {
		return err
	}
	fmt.Printf("Set chamber light to %v\n", state)
	return nil
}

type SpeedCmd struct {
	Mode string `arg:"" enum:"silent,standard,sport,ludicrous" help:"Speed mode"`
}

func (c *SpeedCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	// Map names to protocol values
	speedMap := map[string]string{
		"silent":    bambulan.SpeedSilent,
		"standard":  bambulan.SpeedStandard,
		"sport":     bambulan.SpeedSport,
		"ludicrous": bambulan.SpeedLudicrous,
	}

	val := speedMap[c.Mode]
	if _, err := client.MQTT.SetSpeedProfile(val); err != nil {
		return err
	}
	fmt.Printf("Set speed profile to %s (%s)\n", c.Mode, val)
	return nil
}

type PrintCmd struct {
	Start  PrintStartCmd  `cmd:"" help:"Start a print"`
	Pause  PrintPauseCmd  `cmd:"" help:"Pause current print"`
	Resume PrintResumeCmd `cmd:"" help:"Resume current print"`
	Stop   PrintStopCmd   `cmd:"" help:"Stop current print"`
	Skip   PrintSkipCmd   `cmd:"" help:"Skip objects in current print"`
}

type PrintSkipCmd struct {
	Objects []int `arg:"" help:"List of object IDs to skip"`
}

func (c *PrintSkipCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SkipObjects(c.Objects); err != nil {
		return err
	}
	fmt.Printf("Sent skip objects command for IDs: %v\n", c.Objects)
	return nil
}

type ConfigCmd struct {
	Option         ConfigOptionCmd         `cmd:"" help:"Set print options"`
	MarkerDetector ConfigMarkerDetectorCmd `cmd:"" help:"Configure marker detector"`
	Nozzle         ConfigNozzleCmd         `cmd:"" help:"Configure nozzle details"`
}

type ConfigOptionCmd struct {
	Name    string `arg:"" enum:"auto_recovery,auto_switch_filament,filament_tangle_detect,sound_enable" help:"Option name"`
	Enable  bool   `help:"Enable option" xor:"state"`
	Disable bool   `help:"Disable option" xor:"state"`
}

func (c *ConfigOptionCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	enabled := c.Enable // Disable is implicit false if Enable is false, but xor ensures one
	if c.Disable {
		enabled = false
	}

	if _, err := client.MQTT.SetPrintOption(c.Name, enabled); err != nil {
		return err
	}
	fmt.Printf("Set print option '%s' to %v\n", c.Name, enabled)
	return nil
}

type ConfigMarkerDetectorCmd struct {
	Enable  bool `help:"Enable detector" xor:"state"`
	Disable bool `help:"Disable detector" xor:"state"`
}

func (c *ConfigMarkerDetectorCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	enabled := c.Enable
	if c.Disable {
		enabled = false
	}

	if _, err := client.MQTT.SetBuildPlateMarkerDetector(enabled); err != nil {
		return err
	}
	fmt.Printf("Set marker detector to %v\n", enabled)
	return nil
}

type ConfigNozzleCmd struct {
	Diameter float64 `help:"Nozzle diameter (e.g. 0.4)" required:"" short:"d"`
	Type     string  `help:"Nozzle type (e.g. hardened_steel)" required:"" short:"t"`
}

func (c *ConfigNozzleCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetNozzleDetails(c.Diameter, c.Type); err != nil {
		return err
	}
	fmt.Printf("Set nozzle details: diameter=%.1f, type=%s\n", c.Diameter, c.Type)
	return nil
}

type PrintPauseCmd struct{}

func (c *PrintPauseCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.PausePrint(); err != nil {
		return err
	}
	fmt.Println("Sent pause command")
	return nil
}

type PrintResumeCmd struct{}

func (c *PrintResumeCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.ResumePrint(); err != nil {
		return err
	}
	fmt.Println("Sent resume command")
	return nil
}

type PrintStopCmd struct{}

func (c *PrintStopCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.StopPrint(); err != nil {
		return err
	}
	fmt.Println("Sent stop command")
	return nil
}

type PrintStartCmd struct {
	File                 string `arg:"" help:"G-code or 3MF file to print"`
	BedType              string `help:"Bed type (auto, textured_plate, cool_plate, engineering_plate, high_temp_plate)" default:"auto" short:"b"`
	Timelapse            bool   `help:"Enable timelapse" short:"t"`
	BedLeveling          bool   `help:"Enable bed leveling" default:"true" short:"e"`
	FlowCalibration      bool   `help:"Enable flow calibration" short:"f"`
	VibrationCalibration bool   `help:"Enable vibration calibration" default:"true" short:"V"`
	LayerInspection      bool   `help:"Enable layer inspection" short:"i"`
	UseAMS               *bool  `help:"Use AMS (defaults to true if AMS is present)" short:"a"`
	SkipUpload           bool   `help:"Skip upload, file must exist on printer" default:"false"`
}

func (c *PrintStartCmd) Run(ctx *Context) error {
	client := ctx.Client

	// We need to determine UseAMS value.
	// If the user didn't specify it (c.UseAMS == nil), we need to check the printer status.
	// This requires connecting and waiting for the first status update.

	var useAMS bool
	if c.UseAMS != nil {
		// User explicitly set it
		useAMS = *c.UseAMS
		// We still need to start the client for the print command later
		if err := client.Start(); err != nil {
			return err
		}
	} else {
		// User didn't specify. Auto-detect.
		fmt.Println("Checking for AMS presence...")
		statusChan := make(chan *bambulan.PrinterStatus, 1)
		client.MQTT.OnUpdate = func(status *bambulan.PrinterStatus) {
			select {
			case statusChan <- status:
			default:
			}
		}

		if err := client.Start(); err != nil {
			return err
		}

		select {
		case status := <-statusChan:
			if status.Ams != nil && len(status.Ams.Ams) > 0 {
				useAMS = true
				fmt.Println("-> AMS detected. Enabling AMS.")
			} else {
				useAMS = false
				fmt.Println("-> No AMS detected. Disabling AMS.")
			}
		case <-time.After(10 * time.Second):
			return fmt.Errorf("timeout waiting for printer status to detect AMS")
		}
	}
	// Stop client callback to avoid noise, though client needs to stay connected
	client.MQTT.OnUpdate = nil

	time.Sleep(1 * time.Second)

	localPath := c.File
	var remotePath string

	if !c.SkipUpload {
		// 1. Upload
		remotePath = filepath.Join("/", filepath.Base(localPath))
		fmt.Printf("Uploading %s to printer (remote: %s)...\n", localPath, remotePath)
		uProgressFunc := func(current, total int64) {
			if total > 0 {
				percent := float64(current) / float64(total) * 100
				fmt.Printf("\rUpload: %.1f%% (%d/%d bytes)", percent, current, total)
			} else {
				fmt.Printf("\rUpload: %d bytes", current)
			}
		}

		if err := client.File.UploadFile(localPath, remotePath, uProgressFunc); err != nil {
			fmt.Println()
			return fmt.Errorf("failed to upload file: %w", err)
		}
		fmt.Printf("\nUpload complete.\n")
	} else {
		// If skipping upload, the 'File' argument is treated as the remote path
		remotePath = c.File
		fmt.Printf("Skipping upload. Using existing remote file: %s\n", remotePath)
	}

	// 2. Start Print
	opts := bambulan.PrintOptions{
		BedType:              c.BedType,
		Timelapse:            c.Timelapse,
		BedLeveling:          c.BedLeveling,
		FlowCalibration:      c.FlowCalibration,
		VibrationCalibration: c.VibrationCalibration,
		LayerInspection:      c.LayerInspection,
		UseAMS:               useAMS,
	}

	fmt.Printf("Starting print for %s...\n", remotePath)
	fmt.Printf("Options: BedType=%s, AMS=%v, Leveling=%v, FlowCalibration=%v, VibrationCalibration=%v\n",
		opts.BedType, opts.UseAMS, opts.BedLeveling, opts.FlowCalibration, opts.VibrationCalibration)

	if _, err := client.MQTT.StartPrint(remotePath, opts); err != nil {
		return fmt.Errorf("failed to start print: %w", err)
	}
	fmt.Println("Print started!")
	defer client.Stop()
	return nil
}

type SendGCodeCmd struct {
	GCode string `arg:"" help:"G-code string to send"`
}

func (c *SendGCodeCmd) Run(ctx *Context) error {
	client := ctx.Client
	// Buffer for updates to capture response even if it comes during SendGCode return
	updates := make(chan *bambulan.PrinterStatus, 100)

	// Override update handler to capture response
	client.MQTT.OnUpdate = func(status *bambulan.PrinterStatus) {
		statusCopy := *status
		select {
		case updates <- &statusCopy:
		default:
			// Buffer full, drop update (unlikely with 100 size)
		}
	}

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	fmt.Println("Calling SendGCode...")
	seqID, err := client.MQTT.SendGCode(c.GCode)
	if err != nil {
		return err
	}

	fmt.Println("Waiting for response...")
	timeout := time.After(10 * time.Second)
	for {
		select {
		case status := <-updates:
			if status.SequenceID == seqID {
				if status.Result == "success" {
					fmt.Println("Command success!")
					return nil
				}
				return fmt.Errorf("command failed: result=%s, reason=%s", status.Result, status.Reason)
			}
		case <-timeout:
			return fmt.Errorf("timeout waiting for response")
		}
	}
}

type CaptureCmd struct {
	Output string `arg:"" optional:"" default:"capture.jpg" help:"Output filename"`
}

func (c *CaptureCmd) Run(ctx *Context) error {
	fmt.Println("Capturing frame...")
	frame, err := ctx.Client.Camera.CaptureFrame()
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.Output, frame, 0644); err != nil {
		return err
	}
	fmt.Printf("Saved to %s (%d bytes)\n", c.Output, len(frame))
	return nil
}

type TempCmd struct {
	Head TempHeadCmd `cmd:"" help:"Set nozzle temperature"`
	Bed  TempBedCmd  `cmd:"" help:"Set bed temperature"`
}

type TempHeadCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
}

func (c *TempHeadCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetNozzleTemperature(c.Temperature); err != nil {
		return err
	}
	fmt.Printf("Set nozzle temperature to %d°C\n", c.Temperature)
	return nil
}

type TempBedCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
}

func (c *TempBedCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetBedTemperature(c.Temperature); err != nil {
		return err
	}
	fmt.Printf("Set bed temperature to %d°C\n", c.Temperature)
	return nil
}

type FanCmd struct {
	Arg1  string `arg:"" help:"Fan name (part, aux, chamber, all) OR speed percentage (for all fans)"`
	Speed *int   `arg:"" optional:"" help:"Fan speed percentage (0-100) if first arg is fan name"`
}

func (c *FanCmd) Run(ctx *Context) error {
	var fan string
	var speed int

	// Parse arguments to determine usage pattern
	if s, err := strconv.Atoi(c.Arg1); err == nil {
		// Usage: bambulan fan <speed>
		// Arg1 is a number, treat as speed for "all" fans
		if c.Speed != nil {
			return fmt.Errorf("unexpected second argument '%d' when first argument is speed", *c.Speed)
		}
		fan = "all"
		speed = s
	} else {
		// Usage: bambulan fan <name> <speed>
		// Arg1 is fan name
		fan = strings.ToLower(c.Arg1)
		if c.Speed == nil {
			return fmt.Errorf("missing speed argument")
		}
		speed = *c.Speed
	}

	// Validate fan name
	validFans := map[string]bool{
		"part":    true,
		"aux":     true,
		"chamber": true,
		"all":     true,
	}
	if !validFans[fan] {
		return fmt.Errorf("invalid fan name: '%s'. Construct: bambulan fan <part|aux|chamber|all> <speed>", fan)
	}

	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetFanSpeed(fan, speed); err != nil {
		return err
	}
	fmt.Printf("Set %s fan speed to %d%%\n", fan, speed)
	return nil
}

type FileCmd struct {
	Ls       FileLsCmd       `cmd:"" help:"List files"`
	Download FileDownloadCmd `cmd:"" help:"Download file"`
	Upload   FileUploadCmd   `cmd:"" help:"Upload file"`
	Rm       FileRmCmd       `cmd:"" help:"Remove file or directory"`
	Mkdir    FileMkdirCmd    `cmd:"" help:"Make directory"`
	Mv       FileMvCmd       `cmd:"" help:"Move/Rename file"`
}

type FileLsCmd struct {
	Path      string `arg:"" optional:"" default:"/" help:"Directory to list"`
	Extension string `help:"Filter by extension (e.g. .3mf)" short:"e"`
}

func (c *FileLsCmd) Run(ctx *Context) error {
	fmt.Printf("Listing files in %s...\n", c.Path)
	if c.Extension != "" {
		files, err := ctx.Client.File.GetFiles(c.Path, c.Extension)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Println(f)
		}
	} else {
		files, err := ctx.Client.File.ListFiles(c.Path)
		if err != nil {
			return err
		}
		for _, f := range files {
			t := "FILE"
			if f.Type == "dir" { // Directory
				t = "DIR "
			}
			fmt.Printf("%s %-10d %s\n", t, f.Size, f.Name)
		}
	}
	return nil
}

type FileDownloadCmd struct {
	Remote string `arg:"" help:"Remote file path"`
	Local  string `arg:"" optional:"" help:"Local destination path"`
}

func (c *FileDownloadCmd) Run(ctx *Context) error {
	local := c.Local
	if local == "" {
		local = filepath.Base(c.Remote)
	}

	fmt.Printf("Downloading %s to %s...\n", c.Remote, local)
	start := time.Now()

	progressFunc := func(current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rDownload: %.1f%% (%d/%d bytes)", percent, current, total)
		} else {
			fmt.Printf("\rDownload: %d bytes (unknown total)", current)
		}
	}

	if err := ctx.Client.File.DownloadFile(c.Remote, local, progressFunc); err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\nDownloaded in %v\n", time.Since(start))
	return nil
}

type FileUploadCmd struct {
	Local  string `arg:"" help:"Local file path"`
	Remote string `arg:"" optional:"" help:"Remote destination path"`
}

func (c *FileUploadCmd) Run(ctx *Context) error {
	remote := c.Remote
	if remote == "" {
		remote = "/" + filepath.Base(c.Local)
	}

	fmt.Printf("Uploading %s to %s...\n", c.Local, remote)
	start := time.Now()

	progressFunc := func(current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rUpload: %.1f%% (%d/%d bytes)", percent, current, total)
		} else {
			fmt.Printf("\rUpload: %d bytes", current)
		}
	}

	if err := ctx.Client.File.UploadFile(c.Local, remote, progressFunc); err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\nUploaded in %v\n", time.Since(start))
	return nil
}

type FileRmCmd struct {
	Path      string `arg:"" help:"Path to remove"`
	Recursive bool   `help:"Recursive delete" short:"r"`
}

func (c *FileRmCmd) Run(ctx *Context) error {
	if c.Recursive {
		fmt.Printf("Recursively removing %s...\n", c.Path)
		if err := ctx.Client.File.RemoveAll(c.Path); err != nil {
			return err
		}
	} else {
		fmt.Printf("Removing file %s...\n", c.Path)
		if err := ctx.Client.File.Delete(c.Path); err != nil {
			return err
		}
	}
	fmt.Println("Done.")
	return nil
}

type FileMkdirCmd struct {
	Path string `arg:"" help:"Directory path to create"`
}

func (c *FileMkdirCmd) Run(ctx *Context) error {
	fmt.Printf("Creating directory %s...\n", c.Path)
	if err := ctx.Client.File.MakeDirectory(c.Path); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}

type FileMvCmd struct {
	Source string `arg:"" help:"Source path"`
	Dest   string `arg:"" help:"Destination path"`
}

func (c *FileMvCmd) Run(ctx *Context) error {
	fmt.Printf("Moving %s to %s...\n", c.Source, c.Dest)
	if err := ctx.Client.File.Rename(c.Source, c.Dest); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}

func formatFan(val string) string {
	if val == "" {
		return "0%"
	}
	// Try to parse as integer
	var i int
	_, err := fmt.Sscanf(val, "%d", &i)
	if err != nil {
		return val
	}
	// Convert 0-15 scale to percentage
	pct := float64(i) / 15.0 * 100.0
	return fmt.Sprintf("%.0f%%", pct)
}

type DumpInfoCmd struct{}

func (c *DumpInfoCmd) Run(ctx *Context) error {
	client := ctx.Client
	done := make(chan struct{})

	// Override update handler to print JSON and exit
	client.MQTT.OnUpdate = func(status *bambulan.PrinterStatus) {
		b, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling status: %v\n", err)
			return
		}
		fmt.Println(string(b))
		// We only want one dump
		select {
		case <-done:
		default:
			close(done)
		}
	}

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()

	// DumpInfo is called on connect, but we can also explicitly call it
	if _, err := client.MQTT.DumpInfo(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Waiting for status dump...")

	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for dump info")
	}
}

type AmsCmd struct {
	Filament    AmsFilamentCmd    `cmd:"" help:"Update AMS filament properties"`
	Unload      AmsUnloadCmd      `cmd:"" help:"Unload filament"`
	Load        AmsLoadCmd        `cmd:"" help:"Load filament from a specific slot"`
	Control     AmsControlCmd     `cmd:"" help:"Send AMS control command (resume, pause, reset)"`
	UserSetting AmsUserSettingCmd `cmd:"" help:"Update AMS user settings"`
	KFactor     AmsKFactorCmd     `cmd:"" help:"Set filament K-factor"`
}

type AmsUnloadCmd struct{}

func (c *AmsUnloadCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.UnloadFilament(); err != nil {
		return err
	}
	fmt.Println("Sent unload filament command")
	return nil
}

type AmsLoadCmd struct {
	Target int `arg:"" help:"Target slot ID (0-3 for AMS 1, 4-7 for AMS 2, etc, or 254 for external)"`
}

func (c *AmsLoadCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.LoadFilament(c.Target); err != nil {
		return err
	}
	fmt.Printf("Sent load filament command for slot %d\n", c.Target)
	return nil
}

type AmsControlCmd struct {
	Command string `arg:"" enum:"resume,pause,reset" help:"Control command: resume, pause, reset"`
}

func (c *AmsControlCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SendAMSControlCommand(c.Command); err != nil {
		return err
	}
	fmt.Printf("Sent AMS control command: %s\n", c.Command)
	return nil
}

type AmsUserSettingCmd struct {
	Unit            int  `help:"AMS Unit ID (0-3)" default:"0" short:"u"`
	StartupRead     bool `help:"Update on startup"`
	TrayRead        bool `help:"Update on insert"`
	CalibrateRemain bool `help:"Calibrate remaining capacity"`
}

func (c *AmsUserSettingCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetAMSUserSetting(c.Unit, c.StartupRead, c.TrayRead, c.CalibrateRemain); err != nil {
		return err
	}
	fmt.Printf("Sent AMS user settings update for unit %d\n", c.Unit)
	return nil
}

type AmsKFactorCmd struct {
	Tray int     `help:"Tray ID (0-15)" required:"" short:"t"`
	K    float64 `help:"K Factor" required:"" short:"k"`
	N    float64 `help:"N Coefficient" default:"1.4" short:"n"`
}

func (c *AmsKFactorCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetSpoolKFactor(c.Tray, c.K, c.N); err != nil {
		return err
	}
	fmt.Printf("Sent K-factor update for tray %d: K=%f, N=%f\n", c.Tray, c.K, c.N)
	return nil
}

type AmsFilamentCmd struct {
	Unit       int    `help:"AMS Unit ID (0-3)" default:"0" short:"u"`
	Slot       int    `help:"Slot ID (0-3)" required:"" short:"S"`
	Color      string `help:"Color in HEX (RRGGBBAA). Optional if lookup finds a match." short:"C"`
	Type       string `help:"Filament Type (e.g. 'PLA Basic') or search term" required:"" short:"t"`
	FilamentID string `help:"Filament ID (e.g. 'GFA00'). Optional if Type matches a profile." short:"f"`
	SettingID  string `help:"Setting ID (e.g. 'GFA00_1.75_PLA...'). Optional if Type matches a profile." short:"i"`
	MinTemp    int    `help:"Minimum nozzle temperature" default:"0" short:"n"`
	MaxTemp    int    `help:"Maximum nozzle temperature" default:"0" short:"m"`
	Resources  string `help:"Path to filament JSON resources" type:"path" short:"R" env:"BAMBULAN_RESOURCES"`
}

func (c *AmsFilamentCmd) Run(ctx *Context) error {
	client := ctx.Client
	// Buffer for updates to capture response
	updates := make(chan *bambulan.PrinterStatus, 100)

	// Override update handler to capture response
	client.MQTT.OnUpdate = func(status *bambulan.PrinterStatus) {
		statusCopy := *status
		select {
		case updates <- &statusCopy:
		default:
		}
	}

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	// Check if we need to look up the filament
	if c.FilamentID == "" || c.SettingID == "" {
		if c.Resources == "" {
			return fmt.Errorf("resources path required to resolve filament/setting ID, or provide them manually using --filament-id and --setting-id")
		}

		fmt.Printf("Loading resources from %s...\n", c.Resources)
		fils, err := filament.LoadAll(c.Resources)
		if err != nil {
			return fmt.Errorf("failed to load resources: %w", err)
		}

		// TODO: Infer printer model from discovery, or user flag.
		// For now, we search all, or rely on user being specific.
		matches := filament.Find(fils, c.Type, "")

		if len(matches) == 0 {
			return fmt.Errorf("no filament found matching '%s'", c.Type)
		}

		// Filter for valid setting_id (leaf profiles)
		var validMatches []filament.Filament
		for _, m := range matches {
			if m.SettingID != "" {
				validMatches = append(validMatches, m)
			}
		}

		if len(validMatches) == 0 {
			return fmt.Errorf("no valid profiles (with setting_id) found matching '%s'", c.Type)
		}

		if len(validMatches) == 0 {
			return fmt.Errorf("no valid profiles (with setting_id) found matching '%s'", c.Type)
		}

		// If user provided a specific SettingID or FilamentID, filter by it
		if c.SettingID != "" {
			var filtered []filament.Filament
			for _, m := range validMatches {
				if strings.EqualFold(m.SettingID, c.SettingID) {
					filtered = append(filtered, m)
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("provided --setting-id '%s' does not match any profile for type '%s'", c.SettingID, c.Type)
			}
			validMatches = filtered
		}

		if c.FilamentID != "" {
			var filtered []filament.Filament
			for _, m := range validMatches {
				if strings.EqualFold(m.FilamentID, c.FilamentID) {
					filtered = append(filtered, m)
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("provided --filament-id '%s' does not match any profile for type '%s'", c.FilamentID, c.Type)
			}
			validMatches = filtered
		}

		if len(validMatches) > 1 {
			fmt.Printf("Multiple matches found for '%s':\n", c.Type)
			for _, m := range validMatches {
				fmt.Printf(" - %s (ID: %s, Setting: %s)\n", m.Name, m.FilamentID, m.SettingID)
			}
			return fmt.Errorf("please be more specific or provide --filament-id/--setting-id")
		}

		// Single match found
		match := validMatches[0]
		c.FilamentID = match.FilamentID
		c.SettingID = match.SettingID

		// Use parsed temps if available and override not provided
		if c.MinTemp == 0 && match.TempMin > 0 {
			c.MinTemp = match.TempMin
		} else if c.MinTemp == 0 {
			c.MinTemp = 190 // Fallback
		}

		if c.MaxTemp == 0 && match.TempMax > 0 {
			c.MaxTemp = match.TempMax
		} else if c.MaxTemp == 0 {
			c.MaxTemp = 220 // Fallback
		}

		fmt.Printf("Resolved '%s' to:\n  Name: %s\n  FilamentID: %s\n  SettingID: %s\n", c.Type, match.Name, c.FilamentID, c.SettingID)

		// Attempt to resolve color if missing
		if c.Color == "" {
			colorFile := filepath.Join(c.Resources, "filaments_color_codes.json")
			// We try to load without erroring if file missing, unless color is truly required
			colors, err := filament.LoadColors(colorFile)
			if err == nil {
				if col, ok := colors[c.FilamentID]; ok {
					c.Color = col
					fmt.Printf("  Color (auto): %s\n", c.Color)
				}
			}
		}

		if c.Color == "" {
			return fmt.Errorf("color is required and could not be resolved automatically (filaments_color_codes.json not found or ID not in it)")
		}

		fmt.Printf("  Nozzle Temp: %d-%d °C\n  Bed Temp: %d °C\n", c.MinTemp, c.MaxTemp, match.BedTemp)

		fmt.Print("Proceed? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		response = strings.TrimSpace(response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted by user.")
			return nil
		}
	}

	fmt.Printf("Updating AMS Unit %d Slot %d...\n  Type: %s\n  Color: %s\n  FilamentID: %s\n  SettingID: %s\n  Temp: %d-%d\n",
		c.Unit, c.Slot, c.Type, c.Color, c.FilamentID, c.SettingID, c.MinTemp, c.MaxTemp)

	seqID, err := client.MQTT.SetAMSFilament(c.Unit, c.Slot, c.FilamentID, c.SettingID, c.Color, c.Type, c.MinTemp, c.MaxTemp)
	if err != nil {
		return err
	}
	fmt.Println("Update sent. Waiting for confirmation...")

	timeout := time.After(10 * time.Second)
	for {
		select {
		case status := <-updates:
			if status.SequenceID == seqID {
				if status.Result == "success" {
					fmt.Println("Success!")
					return nil
				}
				return fmt.Errorf("command failed: result=%s, reason=%s", status.Result, status.Reason)
			}
		case <-timeout:
			return fmt.Errorf("timeout waiting for confirmation")
		}
	}
}

func main() {
	ctx := kong.Parse(&cli,
		kong.Name("bambulan"),
		kong.Description("Control your Bambu Lab printer"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			//          Compact: true,
		}),
		kong.Vars{
			"version": version,
		},
	)

	// Logging
	var level slog.Level
	switch cli.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	logger := slog.New(slog.NewTextHandler(os.Stderr, opts))
	slog.SetDefault(logger)

	// Initialize Client.
	// We create the client here but don't start it yet.
	// Individual commands start it if needed.
	// Status updates are handled by the callback injected in StatusCmd.Run.
	client := bambulan.NewClient(cli.Host, cli.Code, cli.Serial, func(status *bambulan.PrinterStatus) {})

	err := ctx.Run(&Context{Client: client})
	ctx.FatalIfErrorf(err)
}
