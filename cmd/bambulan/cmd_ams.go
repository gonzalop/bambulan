package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gonzalop/bambulan/internal/filament"
)

// AmsCmd groups AMS control subcommands.
type AmsCmd struct {
	Filament    AmsFilamentCmd    `cmd:"" help:"Update AMS filament properties"`
	Unload      AmsUnloadCmd      `cmd:"" help:"Unload filament"`
	Load        AmsLoadCmd        `cmd:"" help:"Load filament from a specific slot"`
	Control     AmsControlCmd     `cmd:"" help:"Send AMS control command (resume, pause, reset)"`
	UserSetting AmsUserSettingCmd `cmd:"" help:"Update AMS user settings"`
	KFactor     AmsKFactorCmd     `cmd:"" help:"Set filament K-factor"`
}

// AmsUnloadCmd represents the CLI command to unload filament from the extruder.
type AmsUnloadCmd struct{}

// Run executes the ams unload command.
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

// AmsLoadCmd represents the CLI command to load filament from an AMS slot or external spool.
type AmsLoadCmd struct {
	Target int `arg:"" help:"Target slot ID (0-3 for AMS 1, 4-7 for AMS 2, etc, or 254 for external)"`
}

// Run executes the ams load command.
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

// AmsControlCmd represents the CLI command to send an AMS control action (resume, pause, reset).
type AmsControlCmd struct {
	Command string `arg:"" enum:"resume,pause,reset" help:"Control command: resume, pause, reset"`
}

// Run executes the ams control command.
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

// AmsUserSettingCmd represents the CLI command to update AMS user settings.
type AmsUserSettingCmd struct {
	Unit            int  `help:"AMS Unit ID (0-3)" default:"0" short:"u"`
	StartupRead     bool `help:"Update on startup"`
	TrayRead        bool `help:"Update on insert"`
	CalibrateRemain bool `help:"Calibrate remaining capacity"`
}

// Run executes the ams user-setting command.
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

// AmsKFactorCmd represents the CLI command to set the extrusion calibration K-factor for a tray.
type AmsKFactorCmd struct {
	Tray int     `help:"Tray ID (0-15)" required:"" short:"t"`
	K    float64 `help:"K Factor" required:"" short:"k"`
	N    float64 `help:"N Coefficient" default:"1.4" short:"n"`
}

// Run executes the ams k-factor command.
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

// AmsFilamentCmd represents the CLI command to update AMS filament properties for a slot.
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

// Run executes the ams filament command.
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
