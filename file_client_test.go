package bambulan

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gonzalop/ftp"
)

func TestLocalEntryPath(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name  string
		entry string
		ok    bool
	}{
		{"valid file", "model.gcode", true},
		{"valid subpath", "sub/plate.3mf", true},
		{"name starting with dots", "..cache", true},
		{"parent directory escape", "../etc/passwd", false},
		{"deep escape", "../../home/user/.ssh/id_rsa", false},
		{"dot slash escape", "./../escape.txt", false},
		{"escape through subdir", "sub/../../x", false},
		{"parent itself", "..", false},
		{"current dir", ".", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := localEntryPath(dir, tt.entry)
			if ok != tt.ok {
				t.Fatalf("localEntryPath(%q) ok = %v, want %v", tt.entry, ok, tt.ok)
			}
			if ok && !strings.HasPrefix(p, dir+string(filepath.Separator)) {
				t.Errorf("localEntryPath(%q) = %q, outside %q", tt.entry, p, dir)
			}
		})
	}
}

func TestFileClient_RemotePathJoining(t *testing.T) {
	// Remote FTP paths MUST use forward slashes even on Windows systems
	tests := []struct {
		base     string
		sub      string
		expected string
	}{
		{"/", "cache", "/cache"},
		{"/models", "benchy.gcode", "/models/benchy.gcode"},
		{"/timelapse", "sub/clip.mp4", "/timelapse/sub/clip.mp4"},
	}

	for _, tc := range tests {
		got := path.Join(tc.base, tc.sub)
		if got != tc.expected {
			t.Errorf("path.Join(%q, %q) = %q, want %q", tc.base, tc.sub, got, tc.expected)
		}
		if strings.Contains(got, `\`) {
			t.Errorf("remote path contains backslash: %q", got)
		}
	}
}

func TestListingIsSingleFile(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		entries []*ftp.Entry
		want    bool
	}{
		{"file lists itself", "/a.gcode", []*ftp.Entry{{Name: "a.gcode", Type: "file"}}, true},
		{"server returns full path", "/cache/a.gcode", []*ftp.Entry{{Name: "/cache/a.gcode", Type: "file"}}, true},
		{"directory with one file", "/dir", []*ftp.Entry{{Name: "a.gcode", Type: "file"}}, false},
		{"directory with one subdir", "/dir", []*ftp.Entry{{Name: "sub", Type: "dir"}}, false},
		{"empty directory", "/dir", nil, false},
	}
	for _, tt := range tests {
		if got := listingIsSingleFile(tt.target, tt.entries); got != tt.want {
			t.Errorf("%s: listingIsSingleFile = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestWriteFileAtomically(t *testing.T) {
	tests := []struct {
		name     string
		writeErr error
		want     string
	}{
		{"failure keeps the original", errors.New("550 not found"), "old"},
		{"success replaces it", nil, "new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "model.3mf")
			if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			err := writeFileAtomically(p, func(w io.Writer) error {
				if tt.writeErr != nil {
					_, _ = w.Write([]byte("par"))
					return tt.writeErr
				}
				_, err := w.Write([]byte("new"))
				return err
			})
			if (err != nil) != (tt.writeErr != nil) {
				t.Fatalf("err = %v, want error %v", err, tt.writeErr)
			}
			if b, _ := os.ReadFile(p); string(b) != tt.want {
				t.Errorf("content = %q, want %q", b, tt.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("temp file left behind: %d entries", len(entries))
			}
		})
	}
}

// fakeFTPServer answers the greeting, NOOP and QUIT, and never answers STALL,
// so a client sending it blocks until its connection is closed.
func fakeFTPServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.WriteString(c, "220 ready\r\n")
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					switch cmd := strings.ToUpper(strings.Fields(sc.Text() + " x")[0]); cmd {
					case "QUIT":
						_, _ = io.WriteString(c, "221 bye\r\n")
						return
					case "STALL":
						// no reply
					default:
						_, _ = io.WriteString(c, "200 ok\r\n")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// Cancelling the context must unblock a stuck FTP call, return ctx.Err(), and
// drop the connection so the next call reconnects.
func TestWithContextCancelUnblocksStuckCall(t *testing.T) {
	c, err := ftp.Dial(fakeFTPServer(t), ftp.WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	f := &FileClient{client: c}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- f.withContext(ctx, func(c *ftp.Client) error {
			close(started)
			_, err := c.Quote("STALL")
			return err
		})
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second): // well under the 5s FTP timeout
		t.Fatal("withContext did not return after cancel")
	}
	if f.client != nil {
		t.Error("connection kept after cancel; next call would reuse a closed client")
	}
}
