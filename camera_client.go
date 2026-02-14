package bambulan

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// CameraClient handles the MJPEG camera stream connection over TCP/TLS.
type CameraClient struct {
	// Hostname is the IP or hostname of the printer's camera stream.
	Hostname string
	// AccessCode is the password for the camera stream authentication.
	AccessCode string
	// Port is the TCP/TLS port for the camera stream (default 6000).
	Port int
	// Username is the username for camera stream authentication (default "bblp").
	Username string

	streaming bool
	stopChan  chan struct{}
	mu        sync.Mutex
}

// NewCameraClient creates a new CameraClient.
//
// Parameters:
//   - hostname: The IP address or hostname of the printer.
//   - accessCode: The printer's access code.
func NewCameraClient(hostname, accessCode string) *CameraClient {
	return &CameraClient{
		Hostname:   hostname,
		AccessCode: accessCode,
		Port:       6000,
		Username:   "bblp",
	}
}

// GetRTSPURL returns the authenticated RTSPS URL for the printer's camera stream.
// It accepts an optional `reportedURL` (e.g., from PrinterStatus.IPCam.RTSPURL).
//
// If `reportedURL` is provided (non-empty), it injects the authentication credentials into it.
// If `reportedURL` is empty, it logs a warning and generates a default URL which might not work on all models.
//
// Format: rtsps://bblp:<access_code>@<hostname>:322/streaming/live/1
//
// Example:
//
//	status := client.GetPrinterStatus()
//	var url string
//	if status.IPCam != nil {
//	    // Use the URL reported by the printer (recommended)
//	    url = client.Camera.GetRTSPURL(status.IPCam.RTSPURL)
//	} else {
//	    // Fallback to default guess (logs a warning)
//	    url = client.Camera.GetRTSPURL("")
//	}
func (c *CameraClient) GetRTSPURL(reportedURL string) string {
	if reportedURL != "" {
		// e.g. rtsps://192.168.1.50:322/streaming/live/1
		// We need to inject "bblp:<code >@" after "rtsps://"
		if after, ok := strings.CutPrefix(reportedURL, "rtsps://"); ok {
			withoutScheme := after
			return fmt.Sprintf("rtsps://bblp:%s@%s", c.AccessCode, withoutScheme)
		}
		// Fallback for non-standard schemes or if parsing fails.
		return reportedURL
	}

	slog.Info("RTSP URL not reported by printer; guessing default URL. This might not work if the feature is unsupported.")
	return fmt.Sprintf("rtsps://bblp:%s@%s:322/streaming/live/1", c.AccessCode, c.Hostname)
}

func (c *CameraClient) createAuthPacket() []byte {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint32(0x40))   // '@'\0\0\0
	_ = binary.Write(buf, binary.LittleEndian, uint32(0x3000)) // \0'0'\0\0
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))

	// Write Username (padded to 32 bytes)
	userBytes := []byte(c.Username)
	buf.Write(userBytes)
	for i := len(userBytes); i < 32; i++ {
		buf.WriteByte(0)
	}

	// Write AccessCode (padded to 32 bytes)
	codeBytes := []byte(c.AccessCode)
	buf.Write(codeBytes)
	for i := len(codeBytes); i < 32; i++ {
		buf.WriteByte(0)
	}

	return buf.Bytes()
}

// StartStream connects to the camera via the "Bambu Tunnel" protocol (port 6000)
// and continuously sends new JPEG frames to the onImage callback.
//
// Compatibility:
//   - P1 / A1 Series: This is the primary method for streaming in LAN mode.
//   - X1 Series: Supported as a standard fallback. For higher quality (1080p/30fps),
//     consider using GetRTSPURL() if supported by the network configuration.
//
// This method runs in a new goroutine and will continue until StopStream is called or an error occurs.
//
// Parameters:
//   - onImage: A callback function `func(imageData []byte)` which is invoked for each received JPEG frame.
//
// Example:
//
//	err := client.Camera.StartStream(func(imgData []byte) {
//	    fmt.Printf("Received JPEG frame of %d bytes\n", len(imgData))
//	    // Process or save image data
//	})
//	if err != nil {
//	    log.Println("Error starting stream:", err)
//	}
func (c *CameraClient) StartStream(onImage func([]byte)) error {
	c.mu.Lock()
	if c.streaming {
		c.mu.Unlock()
		return fmt.Errorf("stream already running")
	}
	c.streaming = true
	c.stopChan = make(chan struct{})
	c.mu.Unlock()

	go c.streamLoop(onImage)
	return nil
}

// StopStream stops the camera stream if it is running.
func (c *CameraClient) StopStream() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streaming {
		close(c.stopChan)
		c.streaming = false
	}
}

func (c *CameraClient) streamLoop(onImage func([]byte)) {
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", c.Hostname, c.Port), &tls.Config{
		// Bambu Lab printers use self-signed certificates for their camera stream.
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Error("Camera connection failed", "error", err)
		return
	}
	defer conn.Close()

	if _, err := conn.Write(c.createAuthPacket()); err != nil {
		slog.Error("Auth packet send failed", "error", err)
		return
	}

	jpegStart := []byte{0xff, 0xd8, 0xff, 0xe0}
	jpegEnd := []byte{0xff, 0xd9}

	readBuf := make([]byte, 4096)
	var buffer []byte

	for {
		select {
		case <-c.stopChan:
			return
		default:
			n, err := conn.Read(readBuf)
			if err != nil {
				if err != io.EOF {
					slog.Error("Camera read error", "error", err)
				}
				return
			}
			buffer = append(buffer, readBuf[:n]...)

			for {
				start := bytes.Index(buffer, jpegStart)
				if start == -1 {
					break
				}

				// Look for end *after* start
				endSearch := buffer[start+len(jpegStart):]
				end := bytes.Index(endSearch, jpegEnd)
				if end == -1 {
					break
				}

				// Found a frame
				totalEnd := start + len(jpegStart) + end + len(jpegEnd)
				img := make([]byte, totalEnd-start)
				copy(img, buffer[start:totalEnd])

				onImage(img)

				// Advance buffer
				buffer = buffer[totalEnd:]
			}
		}
	}
}

// CaptureFrame connects to the camera via the "Bambu Tunnel" protocol (port 6000),
// captures a single JPEG frame, and then closes the connection.
//
// Compatibility:
// - P1 / A1 Series: Primary method for fetching snapshots in LAN mode.
// - X1 Series: Supported (typically lower resolution than RTSP).
//
// This is a blocking call that will return once a frame is received or a timeout occurs.
//
// Returns:
//   - A byte slice containing the JPEG image data.
//
// Example:
//
//	imgData, err := client.Camera.CaptureFrame()
//	if err != nil {
//	    log.Println("Error capturing frame:", err)
//	} else {
//	    err = os.WriteFile("frame.jpg", imgData, 0644)
//	    if err != nil {
//	        log.Println("Error saving frame:", err)
//	    }
//	}
func (c *CameraClient) CaptureFrame() ([]byte, error) {
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", c.Hostname, c.Port), &tls.Config{
		// Bambu Lab printers use self-signed certificates for their camera stream.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := conn.Write(c.createAuthPacket()); err != nil {
		return nil, err
	}

	jpegStart := []byte{0xff, 0xd8, 0xff, 0xe0}
	jpegEnd := []byte{0xff, 0xd9}
	var buffer []byte
	readBuf := make([]byte, 4096)

	timeout := time.After(5 * time.Second)

	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for frame")
		default:
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return nil, err
			}
			n, err := conn.Read(readBuf)
			if err != nil {
				return nil, err
			}
			buffer = append(buffer, readBuf[:n]...)

			start := bytes.Index(buffer, jpegStart)
			if start != -1 {
				endSearch := buffer[start+len(jpegStart):]
				end := bytes.Index(endSearch, jpegEnd)
				if end != -1 {
					totalEnd := start + len(jpegStart) + end + len(jpegEnd)
					return buffer[start:totalEnd], nil
				}
			}
		}
	}
}
