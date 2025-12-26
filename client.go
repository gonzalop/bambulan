package bambulan

// Client is the main entry point for the BambuLAN library.
// It acts as a central hub, coordinating interaction with the printer through three specialized clients:
//
//   - MQTT: For real-time status monitoring and sending commands (movement, temperature, print jobs).
//   - Camera: For fetching live video streams or capturing static images.
//   - File: For managing files on the printer's SD card (upload, download, list) via FTPS.
type Client struct {
	// MQTT client for control and status updates.
	MQTT *MQTTClient
	// Camera client for video and image access.
	Camera *CameraClient
	// File client for FTPS operations.
	File *FileClient
	// OnUpdate is a callback invoked whenever a new status message is received from the printer.
	OnUpdate func(*PrinterStatus)
}

// NewClient creates a new BambuLAN Client instance.
//
// Parameters:
//   - hostname: The IP address or hostname of the printer (e.g., "192.168.1.50").
//   - accessCode: The printer's access code, found in the Network settings on the printer's screen.
//   - serial: The printer's serial number (e.g., "01S00A..."), used for MQTT topic subscription.
//   - onUpdate: A callback function invoked whenever a status update is received via MQTT. Can be nil if monitoring is not required.
//
// Example:
//
//	client := bambulan.NewClient("192.168.1.50", "12345678", "01S00A...", func(status *bambulan.PrinterStatus) {
//	    fmt.Printf("Current nozzle temp: %.1f\n", status.NozzleTemp)
//	})
func NewClient(hostname, accessCode, serial string, onUpdate func(*PrinterStatus)) *Client {
	mqttClient := NewMQTTClient(hostname, accessCode, serial, onUpdate) // Pass initial callback
	c := &Client{
		MQTT:     mqttClient,
		Camera:   NewCameraClient(hostname, accessCode),
		File:     NewFileClient(hostname, accessCode),
		OnUpdate: onUpdate,
	}
	return c
}

// GetPrinterStatus returns the most recently received printer status.
// It returns nil if no status has been received yet.
func (c *Client) GetPrinterStatus() *PrinterStatus {
	return c.MQTT.GetPrinterStatus()
}

// Start initiates the MQTT connection and subscribes to the printer's report topic.
// It returns an error if the connection cannot be established or subscription fails.
func (c *Client) Start() error {
	return c.MQTT.Start()
}

// Stop gracefully shuts down the MQTT connection and stops any active camera streams.
func (c *Client) Stop() {
	c.MQTT.Stop()
	c.Camera.StopStream()
}
