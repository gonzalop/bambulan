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
// hostname: Printer IP or hostname.
// accessCode: Printer access code (found in settings).
// serial: Printer serial number.
// onUpdate: Callback function for receiving printer status updates.
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
