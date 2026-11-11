package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/internal/hms"
)

// StatusCmd represents the CLI command for monitoring printer status.
type StatusCmd struct {
	Watch bool `help:"Watch for updates" short:"w"`
}

// Run executes the status command.
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
			status := client.PrinterStatus()
			hasPrint := status.McPrintStage != "" || status.GcodeState != ""
			hasInfo := len(status.Modules) > 0 || status.DeviceModel != ""

			if hasPrint && hasInfo {
				c.printStatus(client, status)
				return nil
			}
		case <-timeout:
			// Timeout, print what we have
			c.printStatus(client, client.MQTT.PrinterStatus())
			return nil
		}
	}
}

func (c *StatusCmd) printStatus(client *bambulan.Client, status *bambulan.PrinterStatus) {
	if c.Watch {
		fmt.Printf("\033[2J\033[H") // Clear screen only in watch mode
	}
	fmt.Println("=== Bambu Printer Status ===")
	caps := bambulan.CapabilitiesForModel(status.DeviceModel)
	name := caps.DisplayName
	if name == "" {
		name = "Unknown Model"
	}
	fmt.Printf("Device Model: %s (%s)\n", status.DeviceModel, name)
	fmt.Println("----------------------------")
	fmt.Printf("Stage:        %s (%s)\n", status.McPrintStage, status.PrintStageName())
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
			codeStr := hms.FormatCode(event.Code, event.Attr)
			desc, _ := hms.Lookup(event.Code, event.Attr)
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
		rtspURL := client.Camera.RTSPURL(status.IPCam.RTSPURL)
		fmt.Printf("RTSP Stream: %s\n", rtspURL)
	}

	fmt.Println("----------------------------")
	if c.Watch {
		fmt.Println("Press Ctrl+C to exit")
	}
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

// SysInfoCmd represents the CLI command to display detailed hardware and network information.
type SysInfoCmd struct{}

// Run executes the sys-info command.
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
			status := client.PrinterStatus()
			hasInfo := len(status.Modules) > 0 || status.DeviceModel != ""
			hasStatus := status.McPrintStage != "" || status.GcodeState != ""

			if hasInfo && hasStatus {
				c.printSysInfo(client, status)
				return nil
			}
		case <-timeout:
			// Timeout, print what we have
			c.printSysInfo(client, client.MQTT.PrinterStatus())
			return nil
		}
	}
}

func (c *SysInfoCmd) printSysInfo(client *bambulan.Client, status *bambulan.PrinterStatus) {
	fmt.Println("=== Bambu Printer System Information ===")
	fmt.Println()

	// 1. Hardware Summary
	caps := bambulan.CapabilitiesForModel(status.DeviceModel)
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
			codeStr := hms.FormatCode(event.Code, event.Attr)
			desc, _ := hms.Lookup(event.Code, event.Attr)
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
