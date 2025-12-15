package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/gonzalop/bambulan"
)

var cli struct {
	Host   string `help:"Printer IP or hostname" env:"BAMBULAN_HOST"`
	Code   string `help:"Access code" env:"BAMBULAN_CODE"`
	Serial string `help:"Printer serial number" env:"BAMBULAN_SERIAL"`
	Level  string `help:"Log level" default:"info" enum:"debug,info,warn,error"`

	Status       StatusCmd       `cmd:"" help:"Monitor printer status"`
	ChamberLight ChamberLightCmd `cmd:"" help:"Turn chamber light on or off"`
	Speed        SpeedCmd        `cmd:"" help:"Set speed: silent, standard, sport, ludicrous"`
	Print        PrintCmd        `cmd:"" help:"Control print: start, pause, resume, stop"`
	SendGCode    SendGCodeCmd    `cmd:"" help:"Send raw G-Code command (single line only)"`
	Capture      CaptureCmd      `cmd:"" help:"Capture camera frame"`
	Ls           LsCmd           `cmd:"" help:"List .3mf files in directory"`
	Download     DownloadCmd     `cmd:"" help:"Download file"`
	Web          WebCmd          `cmd:"" help:"Start web interface"`
}

type Context struct {
	Client *bambulan.Client
}

// Commands

type StatusCmd struct {
	ShowAMS bool `help:"Show AMS status"`
}

func (c *StatusCmd) Run(ctx *Context) error {
	client := ctx.Client
	// For status, we update the callback to print
	client.MQTT.OnUpdate = func(status *bambulan.PrinterStatus) {
		fmt.Printf("\033[2J\033[H") // Clear screen
		fmt.Println("=== Bambu Printer Status ===")
		fmt.Printf("Stage:        %s (%s)\n", status.McPrintStage, status.GetPrintStageName())
		fmt.Printf("Progress:     %d%%\n", status.McPercent)
		fmt.Printf("Remaining:    %d min\n", status.McRemainingTime)
		fmt.Printf("Nozzle Temp:  %.1f / %.1f °C\n", status.NozzleTemp, status.NozzleTargetTemp)
		fmt.Printf("Bed Temp:     %.1f / %.1f °C\n", status.BedTemp, status.BedTargetTemp)
		fmt.Printf("Chamber Temp: %.1f °C\n", status.ChamberTemp)
		fmt.Printf("Fan - Part:   %s\n", status.CoolingFanSpeed)
		fmt.Printf("Fan - Aux:    %s\n", status.BigFan1Speed)
		fmt.Printf("Fan - Chamb:  %s\n", status.BigFan2Speed)
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

		if c.ShowAMS && status.Ams != nil {
			fmt.Println("\n--- AMS Status ---")
			for i, unit := range status.Ams.Ams {
				fmt.Printf("Unit %d: Temp=%s, Humidity=%s\n", i+1, unit.Temp, unit.Humidity)
				for j, tray := range unit.Tray {
					if tray.Id == "" {
						fmt.Printf("  Slot %d: [Empty]\n", j+1)
						continue
					}
					fmt.Printf("  Slot %d: %s %s (%d%%)\n", j+1, tray.TraySubBrands, tray.TrayColor, tray.Remain)
				}
			}
		}
		fmt.Println("----------------------------")
		fmt.Println("Press Ctrl+C to exit")
	}

	// Capture interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()

	<-sigChan

	fmt.Println("\nExiting...")
	return nil
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
	if err := client.MQTT.SetChamberLight(state); err != nil {
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
		"silent":    "1",
		"standard":  "2",
		"sport":     "3",
		"ludicrous": "4",
	}

	val := speedMap[c.Mode]
	if err := client.MQTT.SetSpeedProfile(val); err != nil {
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
}

type PrintPauseCmd struct{}

func (c *PrintPauseCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if err := client.MQTT.PausePrint(); err != nil {
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

	if err := client.MQTT.ResumePrint(); err != nil {
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

	if err := client.MQTT.StopPrint(); err != nil {
		return err
	}
	fmt.Println("Sent stop command")
	return nil
}

type PrintStartCmd struct {
	File                 string `arg:"" help:"G-code or 3MF file to print"`
	BedType              string `help:"Bed type (auto, textured_plate, cool_plate, engineering_plate, high_temp_plate)" default:"auto"`
	Timelapse            bool   `help:"Enable timelapse"`
	BedLeveling          bool   `help:"Enable bed leveling" default:"true"`
	FlowCalibration      bool   `help:"Enable flow calibration"`
	VibrationCalibration bool   `help:"Enable vibration calibration" default:"true"`
	LayerInspection      bool   `help:"Enable layer inspection"`
	UseAMS               bool   `help:"Use AMS"`
}

func (c *PrintStartCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	localPath := c.File
	filename := filepath.Base(localPath)

	// 1. Upload
	fmt.Printf("Uploading %s to printer...\n", localPath)
	uProgressFunc := func(current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rUpload: %.1f%% (%d/%d bytes)", percent, current, total)
		} else {
			fmt.Printf("\rUpload: %d bytes", current)
		}
	}

	if err := client.File.UploadFile(localPath, fmt.Sprintf("/%s", filename), uProgressFunc); err != nil {
		fmt.Println()
		return fmt.Errorf("failed to upload file: %w", err)
	}
	fmt.Printf("\nUpload complete.\n")

	// 2. Start Print
	opts := bambulan.PrintOptions{
		BedType:              c.BedType,
		Timelapse:            c.Timelapse,
		BedLeveling:          c.BedLeveling,
		FlowCalibration:      c.FlowCalibration,
		VibrationCalibration: c.VibrationCalibration,
		LayerInspection:      c.LayerInspection,
		UseAMS:               c.UseAMS,
	}

	fmt.Printf("Starting print for %s...\n", filename)
	fmt.Printf("Options: BedType=%s, AMS=%v, Leveling=%v, FlowCalibration=%v, VibrationCalibration=%v\n",
		opts.BedType, opts.UseAMS, opts.BedLeveling, opts.FlowCalibration, opts.VibrationCalibration)

	if err := client.MQTT.StartPrint(filename, opts); err != nil {
		return fmt.Errorf("failed to start print: %w", err)
	}
	fmt.Println("Print started!")
	return nil
}

type SendGCodeCmd struct {
	GCode string `arg:"" help:"G-code string to send"`
}

func (c *SendGCodeCmd) Run(ctx *Context) error {
	client := ctx.Client
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Stop()
	time.Sleep(1 * time.Second)

	if err := client.MQTT.SendGCode(c.GCode); err != nil {
		return err
	}
	fmt.Printf("Sent G-Code: %s\n", c.GCode)
	return nil
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

type LsCmd struct {
	Path string `arg:"" optional:"" default:"/" help:"Directory to list"`
}

func (c *LsCmd) Run(ctx *Context) error {
	fmt.Printf("Listing .3mf files in %s...\n", c.Path)
	files, err := ctx.Client.File.GetFiles(c.Path, ".3mf")
	if err != nil {
		return err
	}
	for _, f := range files {
		fmt.Println(f)
	}
	return nil
}

type DownloadCmd struct {
	Remote string `arg:"" help:"Remote file path"`
	Local  string `arg:"" optional:"" help:"Local destination path"`
}

func (c *DownloadCmd) Run(ctx *Context) error {
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

func main() {
	ctx := kong.Parse(&cli)

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
