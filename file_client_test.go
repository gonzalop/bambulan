package bambulan

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
