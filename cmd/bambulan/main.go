// Package main provides the CLI tool and web dashboard for BambuLAN.
package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/homeassistant"
)

var (
	version   = "dev"
	buildDate = ""
)

func versionString() string {
	if buildDate != "" {
		return version + " (built " + buildDate + ")"
	}
	return version
}

// ByteSize represents a byte size parsed from human-readable strings (e.g., "50MB", "1GB").
type ByteSize int64

// UnmarshalText parses a human-readable byte size string into a ByteSize value.
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

// Context holds shared dependencies passed to CLI command Run methods.
type Context struct {
	Client *bambulan.Client
}

// SendGCodeCmd represents the CLI command to send raw G-code to the printer.
type SendGCodeCmd struct {
	GCode string `arg:"" help:"G-code string to send"`
}

// Run executes the send-g-code command.
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

// CaptureCmd represents the CLI command to capture a single JPEG camera frame.
type CaptureCmd struct {
	Output string `arg:"" optional:"" default:"capture.jpg" help:"Output filename"`
}

// Run executes the capture command.
func (c *CaptureCmd) Run(ctx *Context) error {
	fmt.Println("Capturing frame...")
	frame, err := ctx.Client.Camera.CaptureFrame(context.Background())
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.Output, frame, 0644); err != nil {
		return err
	}
	fmt.Printf("Saved to %s (%d bytes)\n", c.Output, len(frame))
	return nil
}

// DumpInfoCmd represents the CLI command to dump the full printer status as JSON.
type DumpInfoCmd struct{}

// Run executes the dump-info command.
func (c *DumpInfoCmd) Run(ctx *Context) error {
	client := ctx.Client
	done := make(chan struct{})

	sub := client.Subscribe()
	defer sub.Cancel()

	go func() {
		for status := range sub.C {
			b, err := json.Marshal(status, jsontext.WithIndent("  "))
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

// HACmd represents the CLI command to start the Home Assistant MQTT bridge.
type HACmd struct {
	Broker   string `help:"MQTT broker address (e.g. tcp://192.168.1.100:1883)" env:"BAMBULAN_MQTT_BROKER" required:"" short:"b"`
	User     string `help:"MQTT username" env:"BAMBULAN_MQTT_USER" short:"u"`
	Password string `help:"MQTT password" env:"BAMBULAN_MQTT_PASSWORD" short:"p"`
	Prefix   string `help:"MQTT topic prefix for Home Assistant discovery" env:"BAMBULAN_MQTT_PREFIX" default:"homeassistant"`
}

// Run executes the ha command.
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
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errChan := make(chan error, 1)
	go func() {
		errChan <- bridge.Start(sigCtx)
	}()

	select {
	case <-sigCtx.Done():
		fmt.Println("\nShutting down...")
		return nil
	case err := <-errChan:
		return err
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
			"version": versionString(),
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
