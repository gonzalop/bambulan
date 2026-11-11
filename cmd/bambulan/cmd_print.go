package main

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gonzalop/bambulan"
)

// ChamberLightCmd represents the CLI command to turn the chamber light on or off.
type ChamberLightCmd struct {
	State string `arg:"" enum:"on,off" help:"State: on, off"`
}

// Run executes the chamber-light command.
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

// SpeedCmd represents the CLI command to set the print speed profile.
type SpeedCmd struct {
	Mode string `arg:"" enum:"silent,standard,sport,ludicrous" help:"Speed mode"`
}

// Run executes the speed command.
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

// PrintCmd groups print control subcommands (start, pause, resume, stop, skip).
type PrintCmd struct {
	Start  PrintStartCmd  `cmd:"" help:"Start a print"`
	Pause  PrintPauseCmd  `cmd:"" help:"Pause current print"`
	Resume PrintResumeCmd `cmd:"" help:"Resume current print"`
	Stop   PrintStopCmd   `cmd:"" help:"Stop current print"`
	Skip   PrintSkipCmd   `cmd:"" help:"Skip objects in current print"`
}

// PrintSkipCmd represents the CLI command to skip specific object IDs in the current print.
type PrintSkipCmd struct {
	Objects []int `arg:"" help:"List of object IDs to skip"`
}

// Run executes the print skip command.
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

// ConfigCmd groups printer configuration subcommands.
type ConfigCmd struct {
	Option         ConfigOptionCmd         `cmd:"" help:"Set print options"`
	MarkerDetector ConfigMarkerDetectorCmd `cmd:"" help:"Configure marker detector"`
	Nozzle         ConfigNozzleCmd         `cmd:"" help:"Configure nozzle details"`
}

// ConfigOptionCmd represents the CLI command to toggle a printer option.
type ConfigOptionCmd struct {
	Name    string `arg:"" enum:"auto_recovery,auto_switch_filament,filament_tangle_detect,sound_enable" help:"Option name"`
	Enable  bool   `help:"Enable option" xor:"state"`
	Disable bool   `help:"Disable option" xor:"state"`
}

// Run executes the config option command.
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

// ConfigMarkerDetectorCmd represents the CLI command to configure the build plate marker detector.
type ConfigMarkerDetectorCmd struct {
	Enable  bool `help:"Enable detector" xor:"state"`
	Disable bool `help:"Disable detector" xor:"state"`
}

// Run executes the config marker-detector command.
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

// ConfigNozzleCmd represents the CLI command to configure nozzle diameter and material type.
type ConfigNozzleCmd struct {
	Diameter float64 `help:"Nozzle diameter (e.g. 0.4)" required:"" short:"d"`
	Type     string  `help:"Nozzle type (e.g. hardened_steel)" required:"" short:"t"`
}

// Run executes the config nozzle command.
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

// PrintPauseCmd represents the CLI command to pause the active print.
type PrintPauseCmd struct{}

// Run executes the print pause command.
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

// PrintResumeCmd represents the CLI command to resume a paused print.
type PrintResumeCmd struct{}

// Run executes the print resume command.
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

// PrintStopCmd represents the CLI command to stop the active print.
type PrintStopCmd struct{}

// Run executes the print stop command.
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

// PrintStartCmd represents the CLI command to upload and start a print job.
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

// Run executes the print start command.
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
		defer client.Stop()
	} else {
		// User didn't specify. Auto-detect.
		fmt.Println("Checking for AMS presence...")
		sub = client.Subscribe()

		if err := client.Start(); err != nil {
			return err
		}
		defer client.Stop()

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
		remotePath = path.Join("/", filepath.Base(localPath))
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
	return nil
}

// TempCmd groups temperature control subcommands (head, bed, chamber).
type TempCmd struct {
	Head    TempHeadCmd    `cmd:"" help:"Set nozzle temperature"`
	Bed     TempBedCmd     `cmd:"" help:"Set bed temperature"`
	Chamber TempChamberCmd `cmd:"" help:"Set chamber temperature"`
}

// TempHeadCmd represents the CLI command to set nozzle temperature.
type TempHeadCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
	Tool        int `help:"Tool (extruder) index" default:"0" short:"t"`
}

// Run executes the temp head command.
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

// TempBedCmd represents the CLI command to set bed temperature.
type TempBedCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
}

// Run executes the temp bed command.
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

// TempChamberCmd represents the CLI command to set chamber temperature.
type TempChamberCmd struct {
	Temperature int `arg:"" help:"Temperature in Celsius"`
}

// Run executes the temp chamber command.
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

// FanCmd represents the CLI command to set fan speed.
type FanCmd struct {
	Arg1  string `arg:"" help:"Fan name (part, aux, chamber, all) OR speed percentage (for all fans)"`
	Speed *int   `arg:"" optional:"" help:"Fan speed percentage (0-100) if first arg is fan name"`
}

// Run executes the fan command.
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
