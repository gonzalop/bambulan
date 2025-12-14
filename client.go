package bambulan

// Client is the main entry point for the BambuLAN library.
// It coordinates MQTT, Camera, and File clients.
type Client struct {
	MQTT   *MQTTClient
	Camera *CameraClient
	File   *FileClient
	// OnUpdate is a callback for status updates. It delegates to MQTT.OnUpdate.
	OnUpdate func(*PrinterStatus)
}

// NewClient creates a new BambuLAN Client.
// It requires the printer's hostname (IP), access code (from settings), and serial number.
// The onUpdate callback is invoked whenever a status update is received from the printer.
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

// GetPrinterStatus returns the underlying printer status.
func (c *Client) GetPrinterStatus() *PrinterStatus {
	return c.MQTT.GetPrinterStatus()
}

// Start initiates the MQTT connection and starts listening for status updates.
// It returns an error if the connection fails.
func (c *Client) Start() error {
	return c.MQTT.Start()
}

// Stop gracefully shuts down the MQTT connection and any active camera streams.
func (c *Client) Stop() {
	c.MQTT.Stop()
	c.Camera.StopStream()
}
