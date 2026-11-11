package bambulan

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestExtractFrames(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 0xff, 0xd9}
	tests := []struct {
		name       string
		reads      [][]byte
		wantFrames int
		maxLeft    int
	}{
		{"one frame", [][]byte{jpeg}, 1, 0},
		{"junk without markers is dropped", [][]byte{bytes.Repeat([]byte{0x42}, 1<<20)}, 0, len(jpegStart) - 1},
		{"junk then frame", [][]byte{append(bytes.Repeat([]byte{0x42}, 5000), jpeg...)}, 1, 0},
		{"start marker split across reads", [][]byte{append([]byte{0x42}, jpeg[:2]...), jpeg[2:]}, 1, 0},
		{"two frames in one read", [][]byte{append(append([]byte{}, jpeg...), jpeg...)}, 2, 0},
		{"end marker split across reads", [][]byte{jpeg[:len(jpeg)-1], jpeg[len(jpeg)-1:]}, 1, 0},
		{"frame body over several reads", [][]byte{jpeg[:5], jpeg[5:7], jpeg[7:]}, 1, 0},
		{"unfinished frame over the limit is dropped", [][]byte{jpegStart, make([]byte, maxFrameSize)}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf []byte
			var scanned int
			frames := 0
			for _, r := range tt.reads {
				buf, scanned = extractFrames(append(buf, r...), scanned, func(img []byte) {
					frames++
					if !bytes.Equal(img, jpeg) {
						t.Errorf("frame = %x, want %x", img, jpeg)
					}
				})
			}
			if frames != tt.wantFrames || len(buf) > tt.maxLeft {
				t.Errorf("frames=%d left=%d bytes, want %d frames and <=%d bytes", frames, len(buf), tt.wantFrames, tt.maxLeft)
			}
		})
	}
}

// A host that accepts TCP but never completes the TLS handshake must not hang the caller.
func TestCaptureFrameTimesOutOnStalledHandshake(t *testing.T) {
	orig := cameraDialTimeout
	cameraDialTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cameraDialTimeout = orig })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	testDone := make(chan struct{})
	defer close(testDone)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		<-testDone // accept TCP, never answer the handshake
	}()

	cam := &CameraClient{Hostname: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
	done := make(chan error, 1)
	go func() { _, err := cam.CaptureFrame(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CaptureFrame succeeded against a stalled host")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CaptureFrame hung on the TLS handshake")
	}
}
