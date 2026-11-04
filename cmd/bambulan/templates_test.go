package main

import (
	"os"
	"strings"
	"testing"
)

// Printer-supplied text (filament names, types) must never be inserted as HTML.
func TestTemplatesDoNotInjectPrinterDataAsHTML(t *testing.T) {
	src, err := os.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"modalCurrent.innerHTML",
		"innerHTML = `${tray.",
		"innerHTML = currentFilament",
	} {
		if strings.Contains(string(src), bad) {
			t.Errorf("index.html writes printer data via innerHTML: %q", bad)
		}
	}
}
