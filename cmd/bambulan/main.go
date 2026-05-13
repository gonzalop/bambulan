package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/alecthomas/kong"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/homeassistant"
	"github.com/gonzalop/bambulan/internal/filament"
)

var version = "dev"

// ByteSize represents a size in bytes, but can be unmarshaled from strings like "5MB", "10KB", etc.
type ByteSize int64

func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.ToUpper(string(text))
	var multiplier int64 = 1

	switch {
	case strings.HasSuffix(s, "GB") || strings.HasSuffix(s, "G"):
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "GB"), "G")
	case strings.HasSuffix(s, "MB") || strings.HasSuffix(s, "M"):
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "MB"), "M")
	case strings.HasSuffix(s, "KB") || strings.HasSuffix(s, "K"):
		multiplier = 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "KB"), "K")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}

	val, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid byte size: %q", string(text))
	}

	*b = ByteSize(val * multiplier)
	return nil
}

var cli struct {
	Version kong.VersionFlag `short:"v" help:"Print version"`
	Host    string           `help:"Printer IP or hostname" env:"BAMBULAN_HOST" short:"H"`
	Code    string           `help:"Access code" env:"BAMBULAN_CODE" short:"c"`
	Serial  string           `help:"Printer serial number" env:"BAMBULAN_SERIAL" short:"s"`
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
	SysInfo      SysInfoCmd      `cmd:"" help:"Display detailed hardware and network information"`
	HA           HACmd           `cmd:"" help:"Start Home Assistant MQTT bridge"`
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

	sub := client.Subscribe()
	defer sub.Cancel()

	go func() {
		for status := range sub.C {
			if c.Watch {
				c.printStatus(client, status)
			}
			select {
			case updateReceived <- struct{}{}:
			default:
			}
		}
	}()

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
				c.printStatus(client, status)
				return nil
			}
		case <-timeout:
			// Timeout, print what we have
			c.printStatus(client, client.MQTT.GetPrinterStatus())
			return nil
		}
	}
}

func (c *StatusCmd) printStatus(client *bambulan.Client, status *bambulan.PrinterStatus) {
	if c.Watch {
		fmt.Printf("\033[2J\033[H") // Clear screen only in watch mode
	}
	fmt.Println("=== Bambu Printer Status ===")
	caps := bambulan.GetPrinterCapabilities(status.DeviceModel)
	name := caps.DisplayName
	if name == "" {
		name = "Unknown Model"
	}
	fmt.Printf("Device Model: %s (%s)\n", status.DeviceModel, name)
	fmt.Println("----------------------------")
	fmt.Printf("Stage:        %s (%s)\n", status.McPrintStage, status.GetPrintStageName())
	fmt.Printf("Progress:     %d%%\n", status.McPercent)
	fmt.Printf("Remaining:    %d min\n", status.McRemainingTime)
	fmt.Printf("Layer:        %d / %d\n", status.LayerNum, status.TotalLayerNum)
	fmt.Printf("Nozzle Temp:  %.1f / %.1f °C (Limit: %d°C)\n", status.NozzleTemp, status.NozzleTargetTemp, status.NozzleTempLimit)
	fmt.Printf("Bed Temp:     %.1f / %.1f °C (Limit: %d°C)\n", status.BedTemp, status.BedTargetTemp, status.BedTempLimit)
	if status.ChamberTemp > 5 {
		fmt.Printf("Chamber Temp: %.1f °C\n", status.ChamberTemp)
	}
	fmt.Printf("Fan - Part:   %s\n", formatFan(status.CoolingFanSpeed))
	if caps.HasAuxFan {
		fmt.Printf("Fan - Aux:    %s\n", formatFan(status.BigFan1Speed))
	}
	if caps.HasChamberFan {
		fmt.Printf("Fan - Chamb:  %s\n", formatFan(status.BigFan2Speed))
	}
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
	fmt.Printf("Speed Lvl:    %d\n", status.SpdLvl)

	if len(status.Hms) > 0 {
		fmt.Println("\n--- ACTIVE ERRORS (HMS) ---")
		for _, event := range status.Hms {
			codeStr := bambulan.FormatHMSCode(event.Code, event.Attr)
			desc, _ := bambulan.LookupHMS(event.Code, event.Attr)
			if desc == "" {
				desc = "Unknown Error"
			}
			fmt.Printf("[!] %s: %s\n", codeStr, desc)
			fmt.Printf("    Troubleshooting: %s\n", event.WikiURL())
		}
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
			raw := unit.HumidityRaw
			if raw != "" {
				if r, err := strconv.Atoi(raw); err == nil {
					if r >= 10 && r <= 100 {
						hum = fmt.Sprintf("%s (%d%%)", hum, r)
					}
				}
			}

			switch hum {
			case "1":
				hum = "1 (Dry)"
			case "5":
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

				if tray.ID == "" {
					fmt.Printf(" %sSlot %d: [Empty]\n", marker, j)
					continue
				}
				remain := ""
				if caps.HasAMSCapacityReporting {
					remain = fmt.Sprintf(" (%d%%)", tray.Remain)
					if tray.Remain < 0 {
						remain = " (Capacity: N/A)"
					}
				}

				name := tray.TraySubBrands
				if name == "" {
					name = tray.TrayType
				}
				if name == "" {
					name = tray.TrayIDName
				}
				fmt.Printf(" %sSlot %d: %s %s%s\n", marker, j, name, tray.TrayColor, remain)
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

	// Camera Information
	if status.IPCam != nil && status.IPCam.RTSPURL != "" {
		fmt.Println("\n--- Camera ---")
		// Use the helper to get the authenticated URL
		rtspURL := client.Camera.GetRTSPURL(status.IPCam.RTSPURL)
		fmt.Printf("RTSP Stream: %s\n", rtspURL)
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
	if _, err := client.MQTT.SetChamberLight(context.Background(), state); err != nil {
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
	if _, err := client.MQTT.SetSpeedProfile(context.Background(), val); err != nil {
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

	if _, err := client.MQTT.SkipObjects(context.Background(), c.Objects); err != nil {
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

	if _, err := client.MQTT.SetPrintOption(context.Background(), c.Name, enabled); err != nil {
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

	if _, err := client.MQTT.SetBuildPlateMarkerDetector(context.Background(), enabled); err != nil {
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

	if _, err := client.MQTT.SetNozzleDetails(context.Background(), c.Diameter, c.Type); err != nil {
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

	if _, err := client.MQTT.PausePrint(context.Background()); err != nil {
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

	if _, err := client.MQTT.ResumePrint(context.Background()); err != nil {
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

	if _, err := client.MQTT.StopPrint(context.Background()); err != nil {
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
	var sub *bambulan.EventSubscription
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
		sub = client.Subscribe()

		if err := client.Start(); err != nil {
			return err
		}

		// Wait for AMS detection.
		// Usually we get a few messages, the first ones might be partial.
		// We stop as soon as we see an AMS or get a full status (pushall).
		timeout := time.After(3 * time.Second)
	waitLoop:
		for {
			select {
			case status := <-sub.C:
				if status.Ams != nil && len(status.Ams.Ams) > 0 {
					useAMS = true
					fmt.Println("-> AMS detected. Enabling AMS.")
					break waitLoop
				}
				if status.Command == "pushall" {
					useAMS = false
					fmt.Println("-> No AMS detected. Disabling AMS.")
					break waitLoop
				}
			case <-timeout:
				useAMS = false
				fmt.Println("-> No AMS detected (timeout). Disabling AMS.")
				break waitLoop
			}
		}
	}
	// Stop client callback to avoid noise, though client needs to stay connected
	if sub != nil {
		sub.Cancel()
	}

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

		if err := client.File.UploadFile(context.Background(), localPath, remotePath, uProgressFunc); err != nil {
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

	if _, err := client.MQTT.StartPrint(context.Background(), remotePath, opts); err != nil {
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
	sub := client.Subscribe()
	defer sub.Cancel()
	updates := sub.C

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	fmt.Println("Calling SendGCode...")
	seqID, err := client.MQTT.SendGCode(context.Background(), c.GCode)
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
	Head    TempHeadCmd    `cmd:"" help:"Set nozzle temperature"`
	Bed     TempBedCmd     `cmd:"" help:"Set bed temperature"`
	Chamber TempChamberCmd `cmd:"" help:"Set chamber temperature"`
}

type TempHeadCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
	Tool        int `help:"Tool (extruder) index" default:"0" short:"t"`
}

func (c *TempHeadCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetNozzleTemperature(context.Background(), c.Temperature, c.Tool); err != nil {
		return err
	}
	fmt.Printf("Set nozzle %d temperature to %d°C\n", c.Tool, c.Temperature)
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

	if _, err := client.MQTT.SetBedTemperature(context.Background(), c.Temperature); err != nil {
		return err
	}
	fmt.Printf("Set bed temperature to %d°C\n", c.Temperature)
	return nil
}

type TempChamberCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
}

func (c *TempChamberCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if _, err := client.MQTT.SetChamberTemperature(context.Background(), c.Temperature); err != nil {
		return err
	}
	fmt.Printf("Set chamber temperature to %d°C\n", c.Temperature)
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

	if _, err := client.MQTT.SetFanSpeed(context.Background(), fan, speed); err != nil {
		return err
	}
	fmt.Printf("Set %s fan speed to %d%%\n", fan, speed)
	return nil
}

type FileCmd struct {
	Ls          FileLsCmd          `cmd:"" help:"List files"`
	Download    FileDownloadCmd    `cmd:"" help:"Download file"`
	DownloadDir FileDownloadDirCmd `cmd:"" help:"Download directory"`
	Upload      FileUploadCmd      `cmd:"" help:"Upload file"`
	Rm          FileRmCmd          `cmd:"" help:"Remove file or directory"`
	Mkdir       FileMkdirCmd       `cmd:"" help:"Make directory"`
	Mv          FileMvCmd          `cmd:"" help:"Move/Rename file"`
}

type FileLsCmd struct {
	Path      string `arg:"" optional:"" default:"/" help:"Directory to list"`
	Extension string `help:"Filter by extension (e.g. .3mf)" short:"e"`
}

func (c *FileLsCmd) Run(ctx *Context) error {
	fmt.Printf("Listing files in %s...\n", c.Path)
	if c.Extension != "" {
		files, err := ctx.Client.File.GetFiles(context.Background(), c.Path, c.Extension)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Println(f)
		}
	} else {
		files, err := ctx.Client.File.ListFiles(context.Background(), c.Path)
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

	if err := ctx.Client.File.DownloadFile(context.Background(), c.Remote, local, progressFunc); err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\nDownloaded in %v\n", time.Since(start))
	return nil
}

type FileDownloadDirCmd struct {
	Remote    string `arg:"" help:"Remote directory path"`
	Local     string `arg:"" help:"Local destination directory path"`
	Recursive bool   `help:"Recursive download" short:"r"`
}

func (c *FileDownloadDirCmd) Run(ctx *Context) error {
	fmt.Printf("Downloading directory %s to %s (recursive: %v)...\n", c.Remote, c.Local, c.Recursive)
	start := time.Now()

	progressFunc := func(filename string, current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rDownload [%s]: %.1f%% (%d/%d bytes)          ", filename, percent, current, total)
		} else {
			fmt.Printf("\rDownload [%s]: %d bytes (unknown total)          ", filename, current)
		}
	}

	if err := ctx.Client.File.DownloadDirectory(context.Background(), c.Remote, c.Local, c.Recursive, progressFunc); err != nil {
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

	if err := ctx.Client.File.UploadFile(context.Background(), c.Local, remote, progressFunc); err != nil {
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
		if err := ctx.Client.File.RemoveAll(context.Background(), c.Path); err != nil {
			return err
		}
	} else {
		fmt.Printf("Removing file %s...\n", c.Path)
		if err := ctx.Client.File.Delete(context.Background(), c.Path); err != nil {
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
	if err := ctx.Client.File.MakeDirectory(context.Background(), c.Path); err != nil {
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
	if err := ctx.Client.File.Rename(context.Background(), c.Source, c.Dest); err != nil {
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

	sub := client.Subscribe()
	defer sub.Cancel()

	go func() {
		for status := range sub.C {
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
	}()

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()

	// DumpInfo is called on connect, but we can also explicitly call it
	if _, err := client.MQTT.DumpInfo(context.Background()); err != nil {
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

type SysInfoCmd struct{}

func (c *SysInfoCmd) Run(ctx *Context) error {
	client := ctx.Client

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()

	// Wait for "complete" update or timeout
	// "Complete" means we have info (from get_version)
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			status := client.GetPrinterStatus()
			hasInfo := len(status.Modules) > 0 || status.DeviceModel != ""
			hasStatus := status.McPrintStage != "" || status.GcodeState != ""

			if hasInfo && hasStatus {
				c.printSysInfo(client, status)
				return nil
			}
		case <-timeout:
			// Timeout, print what we have
			c.printSysInfo(client, client.MQTT.GetPrinterStatus())
			return nil
		}
	}
}

func (c *SysInfoCmd) printSysInfo(client *bambulan.Client, status *bambulan.PrinterStatus) {
	fmt.Println("=== Bambu Printer System Information ===")
	fmt.Println()

	// 1. Hardware Summary
	caps := bambulan.GetPrinterCapabilities(status.DeviceModel)
	modelName := caps.DisplayName
	if modelName == "" {
		modelName = "Unknown Model (" + status.DeviceModel + ")"
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HARDWARE SUMMARY")
	fmt.Fprintf(w, "Model:\t%s\n", modelName)
	fmt.Fprintf(w, "Serial Number:\t%s\n", client.MQTT.Serial)
	fmt.Fprintf(w, "Host/IP:\t%s\n", client.MQTT.Hostname)

	wifi := status.WifiSignal
	if wifi == "" {
		wifi = "Unknown"
	}
	fmt.Fprintf(w, "WiFi Signal:\t%s\n", wifi)
	w.Flush()
	fmt.Println()

	// 2. Capabilities
	fmt.Fprintln(w, "CAPABILITIES")
	fmt.Fprintf(w, "Nozzle Max Temp:\t%d°C\n", caps.MaxNozzleTemp)
	fmt.Fprintf(w, "Bed Max Temp:\t%d°C\n", caps.MaxBedTemp)
	fmt.Fprintf(w, "Chamber Fan:\t%v\n", caps.HasChamberFan)
	fmt.Fprintf(w, "Aux Fan:\t%v\n", caps.HasAuxFan)
	fmt.Fprintf(w, "AMS Support:\t%v\n", caps.HasAMSHumidity || caps.HasAMSCapacityReporting)
	fmt.Fprintf(w, "Timelapse:\t%v\n", caps.HasTimelapse)
	w.Flush()
	fmt.Println()

	// 3. Versions (Modules)
	if len(status.Modules) > 0 {
		fmt.Fprintln(w, "MODULE\tPROJECT\tSW VER\tHW VER")
		for _, m := range status.Modules {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, m.Project, m.SwVer, m.HwVer)
		}
		w.Flush()
		fmt.Println()
	}

	// 4. HMS Status
	if len(status.Hms) > 0 {
		fmt.Fprintln(w, "ACTIVE ERRORS (HMS)")
		for _, event := range status.Hms {
			codeStr := bambulan.FormatHMSCode(event.Code, event.Attr)
			desc, _ := bambulan.LookupHMS(event.Code, event.Attr)
			if desc == "" {
				desc = "Unknown Error"
			}
			fmt.Fprintf(w, "%s\t%s\n", codeStr, desc)
			fmt.Fprintf(w, "\tTroubleshooting: %s\n", event.WikiURL())
		}
		w.Flush()
		fmt.Println()
	}

	// 5. AMS Status
	if status.Ams != nil {
		fmt.Fprintln(w, "AMS STATUS")
		if len(status.Ams.Ams) == 0 {
			fmt.Fprintf(w, "Status:\tDetected (no units reported yet)\n")
		} else {
			for i, unit := range status.Ams.Ams {
				hum := unit.Humidity
				if hum == "" {
					hum = "N/A"
				}
				fmt.Fprintf(w, "Unit %d Humidity:\t%s\n", i, hum)
				for j, tray := range unit.Tray {
					filament := tray.TrayType
					if filament == "" {
						filament = "Empty"
					}
					fmt.Fprintf(w, "  Slot %d:\t%s (%d%%)\n", j+1, filament, tray.Remain)
				}
			}
		}
		w.Flush()
	}
}

type HACmd struct {
	Broker   string `help:"MQTT broker address (e.g. tcp://192.168.1.100:1883)" env:"BAMBULAN_MQTT_BROKER" required:"" short:"b"`
	User     string `help:"MQTT username" env:"BAMBULAN_MQTT_USER" short:"u"`
	Password string `help:"MQTT password" env:"BAMBULAN_MQTT_PASSWORD" short:"p"`
	Prefix   string `help:"MQTT topic prefix for Home Assistant discovery" env:"BAMBULAN_MQTT_PREFIX" default:"homeassistant"`
}

func (c *HACmd) Run(ctx *Context) error {
	client := ctx.Client

	bridge, err := homeassistant.NewBridge(client, c.Broker, c.User, c.Password, c.Prefix)
	if err != nil {
		return err
	}
	defer bridge.Close()

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()

	slog.Info("Starting Home Assistant bridge", "broker", c.Broker, "printer", client.MQTT.Hostname)

	// Handle interrupt for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		errChan <- bridge.Start(context.Background())
	}()

	select {
	case <-sigChan:
		fmt.Println("\nShutting down...")
		return nil
	case err := <-errChan:
		return err
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

	if _, err := client.MQTT.UnloadFilament(context.Background()); err != nil {
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

	if _, err := client.MQTT.LoadFilament(context.Background(), c.Target); err != nil {
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

	if _, err := client.MQTT.SendAMSControlCommand(context.Background(), c.Command); err != nil {
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

	if _, err := client.MQTT.SetAMSUserSetting(context.Background(), c.Unit, c.StartupRead, c.TrayRead, c.CalibrateRemain); err != nil {
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

	if _, err := client.MQTT.SetSpoolKFactor(context.Background(), c.Tray, c.K, c.N); err != nil {
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
	sub := client.Subscribe()
	defer sub.Cancel()
	updates := sub.C

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

	seqID, err := client.MQTT.SetAMSFilament(context.Background(), c.Unit, c.Slot, c.FilamentID, c.SettingID, c.Color, c.Type, c.MinTemp, c.MaxTemp)
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

	// Validate required flags for non-web commands
	// The "web" command manages printer connection via session login, so it doesn't need these global flags.
	// All other commands (status, print, etc.) require a direct connection.
	cmdName := ctx.Command()
	if !strings.HasPrefix(cmdName, "web") {
		var missing []string
		if cli.Host == "" {
			missing = append(missing, "--host")
		}
		if cli.Code == "" {
			missing = append(missing, "--code")
		}
		if cli.Serial == "" {
			missing = append(missing, "--serial")
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "Error: missing required flags for command '%s': %s\n", cmdName, strings.Join(missing, ", "))
			os.Exit(1)
		}
	}

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
	client := bambulan.NewClient(cli.Host, cli.Code, cli.Serial)

	err := ctx.Run(&Context{Client: client})
	ctx.FatalIfErrorf(err)
}
