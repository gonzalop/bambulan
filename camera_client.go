package bambulan

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// CameraClient handles the MJPEG camera stream connection over TCP/TLS.
type CameraClient struct {
	Hostname   string
	AccessCode string
	Port       int
	Username   string
	streaming  bool
	stopChan   chan struct{}
	mu         sync.Mutex
}

// NewCameraClient creates a new CameraClient.
func NewCameraClient(hostname, accessCode string) *CameraClient {
	return &CameraClient{
		Hostname:   hostname,
		AccessCode: accessCode,
		Port:       6000,
		Username:   "bblp",
	}
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

// StartStream connects to the camera and continuously sends new JPEG frames to the onImage callback.
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
					// Keep searching, but maybe discard strictly unusable prefix if buffer gets huge?
					// For now, simple greedy approach as per Python
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

// CaptureFrame connects to the camera, captures a single frame, and closes the connection.
// It returns the JPEG byte slice or an error.
func (c *CameraClient) CaptureFrame() ([]byte, error) {
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", c.Hostname, c.Port), &tls.Config{
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
