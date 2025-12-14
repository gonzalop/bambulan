/*
Package bambulan provides a client library for interacting with Bambu Lab 3D printers over the local area network (LAN).

It supports:
- MQTT for status monitoring and command control (printing, lights, speed).
- FTPS for file management (listing, uploading, downloading).
- Camera access for frame capture.

Example usage:

	client := bambulan.NewClient("192.168.1.100", "access_code", "serial_number", func(status *bambulan.PrinterStatus) {
		fmt.Printf("Progress: %d%%\n", status.McPercent)
	})

	if err := client.Start(); err != nil {
		log.Fatal(err)
	}
	defer client.Stop()

	// ... interaction ...
*/
package bambulan
