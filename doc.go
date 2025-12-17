/*
Package bambulan provides a client library for interacting with Bambu Lab 3D printers over the local area network (LAN).

It supports:
- MQTT for status monitoring and command control (printing, lights, speed).
- FTPS for file management (listing, uploading, downloading).
- Camera access for frame capture.

Example usage:

	import (
		"fmt"
		"log"
		"github.com/gonzalop/bambulan" // Assuming this is how the package is imported
	)

	func main() {
		hostname := "192.168.1.100" // Printer's IP address or hostname
		accessCode := "your_access_code" // Found in printer settings
		serial := "your_printer_serial" // Printer's serial number

		// Initialize the client with a status update callback
		client := bambulan.NewClient(hostname, accessCode, serial, func(status *bambulan.PrinterStatus) {
			fmt.Printf("Nozzle: %.1f°C | Bed: %.1f°C | Progress: %d%%\n",
				status.NozzleTemp, status.BedTemp, status.McPercent)
		})

		// Start the client (connects to MQTT broker)
		if err := client.Start(); err != nil {
			log.Fatalf("Failed to connect to printer: %v", err)
		}
		defer client.Stop() // Ensure connection is closed when main exits

		// Example: Turn on chamber light
		if _, err := client.MQTT.SetChamberLight(true); err != nil {
			log.Printf("Error setting chamber light: %v", err)
		} else {
			fmt.Println("Chamber light turned on.")
		}

		// Keep running to receive updates (or perform other operations)
		select {}
	}
*/
package bambulan
