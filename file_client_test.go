package bambulan

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gonzalop/ftp"
)

func TestFileClient_PathTraversalDefense(t *testing.T) {
	tempDir := t.TempDir()

	testEntries := []struct {
		name      string
		entryName string
		isSecure  bool
	}{
		{"valid file", "model.gcode", true},
		{"valid subpath", "sub/plate.3mf", true},
		{"parent directory escape", "../etc/passwd", false},
		{"deep escape", "../../home/user/.ssh/id_rsa", false},
		{"dot slash escape", "./../escape.txt", false},
	}

	for _, tc := range testEntries {
		t.Run(tc.name, func(t *testing.T) {
			cleanName := filepath.Clean(filepath.FromSlash(tc.entryName))
			if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
				if tc.isSecure {
					t.Fatalf("expected entry %q to be allowed, but marked insecure", tc.entryName)
				}
				return
			}

			localPath := filepath.Join(tempDir, cleanName)
			rel, err := filepath.Rel(tempDir, localPath)
			allowed := err == nil && !strings.HasPrefix(rel, "..")

			if allowed != tc.isSecure {
				t.Errorf("entry %q: got allowed=%v, want %v", tc.entryName, allowed, tc.isSecure)
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

func TestFileClient_AfterFuncCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var called atomic.Bool
	stop := context.AfterFunc(ctx, func() {
		called.Store(true)
	})

	// When download completes, stop() should deregister the AfterFunc callback
	stop()

	// Cancel context afterwards
	cancel()
	time.Sleep(10 * time.Millisecond)

	if called.Load() {
		t.Errorf("expected AfterFunc callback not to be executed after stop()")
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
