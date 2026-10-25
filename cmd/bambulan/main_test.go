package main

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintStartCmd_RemotePath(t *testing.T) {
	localFile := filepath.Join("some", "local", "dir", "plate_1.gcode.3mf")
	remote := path.Join("/", filepath.Base(localFile))
	expected := "/plate_1.gcode.3mf"
	if remote != expected {
		t.Errorf("remote path = %q, want %q", remote, expected)
	}
	if strings.Contains(remote, `\`) {
		t.Errorf("remote path should not contain backslashes: %q", remote)
	}
}
