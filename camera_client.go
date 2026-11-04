package bambulan

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
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

// maxFrameSize bounds the bytes kept while waiting for a frame to end, so a
// stream that never completes a frame can't grow memory without limit. It is
// kept small because each read rescans the unfinished frame for the end marker;
// real frames are a few hundred KB.
const maxFrameSize = 2 << 20

// cameraDialTimeout covers TCP connect and TLS handshake. A var so tests can shorten it.
var cameraDialTimeout = 5 * time.Second

var (
	jpegStart = []byte{0xff, 0xd8, 0xff, 0xe0}
	jpegEnd   = []byte{0xff, 0xd9}
)

// dial connects to the camera. The timeout covers both TCP connect and the TLS
// handshake, so a host that accepts but never answers can't block forever.
func (c *CameraClient) dial() (*tls.Conn, error) {
	return tls.DialWithDialer(&net.Dialer{Timeout: cameraDialTimeout}, "tcp",
		net.JoinHostPort(c.Hostname, strconv.Itoa(c.Port)), &tls.Config{
			// Bambu Lab printers use self-signed certificates for their camera stream.
			InsecureSkipVerify: true,
		})
}

// extractFrames calls emit with each complete JPEG frame in buf and returns
// the bytes to keep for the next read: at most a partial start marker, or one
// unfinished frame up to maxFrameSize.
func extractFrames(buf []byte, emit func([]byte)) []byte {
	for {
		start := bytes.Index(buf, jpegStart)
		if start == -1 {
			// Keep only what could be the beginning of a start marker.
			keep := min(len(buf), len(jpegStart)-1)
			return append([]byte(nil), buf[len(buf)-keep:]...)
		}
		buf = buf[start:]
		end := bytes.Index(buf[len(jpegStart):], jpegEnd)
		if end == -1 {
			if len(buf) > maxFrameSize {
				return nil // no end in sight; drop it and resync on the next start marker
			}
			return buf
		}
		total := len(jpegStart) + end + len(jpegEnd)
		img := make([]byte, total)
		copy(img, buf[:total])
		emit(img)
		buf = buf[total:]
	}
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
	stopChan := c.stopChan
	c.mu.Unlock()

	go c.streamLoop(stopChan, onImage)
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

func (c *CameraClient) streamLoop(stopChan chan struct{}, onImage func([]byte)) {
	defer func() {
		c.mu.Lock()
		if c.stopChan == stopChan {
			c.streaming = false
		}
		c.mu.Unlock()
	}()

	conn, err := c.dial()
	if err != nil {
		slog.Error("Camera connection failed", "error", err)
		return
	}
	defer conn.Close()

	if _, err := conn.Write(c.createAuthPacket()); err != nil {
		slog.Error("Auth packet send failed", "error", err)
		return
	}

	readBuf := make([]byte, 4096)
	var buffer []byte

	for {
		select {
		case <-stopChan:
			return
		default:
			// Set a read deadline so we don't block indefinitely and can check stopChan
			if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				slog.Error("Failed to set camera read deadline", "error", err)
				return
			}

			n, err := conn.Read(readBuf)
			if err != nil {
				if os.IsTimeout(err) {
					// Timeout is expected, just loop back and check stopChan
					continue
				}
				if err != io.EOF {
					slog.Error("Camera read error", "error", err)
				}
				return
			}
			buffer = extractFrames(append(buffer, readBuf[:n]...), onImage)
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
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := conn.Write(c.createAuthPacket()); err != nil {
		return nil, err
	}

	var buffer []byte
	var frame []byte
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
			buffer = extractFrames(append(buffer, readBuf[:n]...), func(img []byte) {
				if frame == nil {
					frame = img
				}
			})
			if frame != nil {
				return frame, nil
			}
		}
	}
}
