package hms

import (
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name     string
		code     uint32
		attr     uint32
		wantDesc string
		wantOk   bool
	}{
		{
			name:     "Valid Heatbed Error",
			code:     0x03000100,
			attr:     0x00010003,
			wantDesc: "The heatbed temperature is abnormal; the heater is over temperature.",
			wantOk:   true,
		},
		{
			name:     "Valid Front Cover Error",
			code:     0x03001200,
			attr:     0x00020001,
			wantDesc: "The front cover of the toolhead fell off.",
			wantOk:   true,
		},
		{
			name:     "Newly Imported Chamber Heating Error",
			code:     0x03009000,
			attr:     0x00010005,
			wantDesc: "Chamber heating failed. The thermal resistance is too high.",
			wantOk:   true,
		},
		{
			name:     "Non-existent Code",
			code:     0xFFFFFFFF,
			attr:     0xFFFFFFFF,
			wantDesc: "",
			wantOk:   false,
		},
		{
			name:     "Partial Match (Wrong Attr)",
			code:     0x03000100,
			attr:     0x00000000,
			wantDesc: "",
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDesc, gotOk := Lookup(tt.code, tt.attr)
			if gotOk != tt.wantOk {
				t.Errorf("Lookup() gotOk = %v, want %v", gotOk, tt.wantOk)
			}
			if gotDesc != tt.wantDesc {
				t.Errorf("Lookup() gotDesc = %v, want %v", gotDesc, tt.wantDesc)
			}
		})
	}
}

func TestFormatCode(t *testing.T) {
	got := FormatCode(0x03000100, 0x00010003)
	want := "0300-0100-0001-0003"
	if got != want {
		t.Errorf("FormatCode() = %v, want %v", got, want)
	}
}

func TestWikiURL(t *testing.T) {
	got := WikiURL(0x03000100, 0x00010003)
	want := "https://wiki.bambulab.com/en/x1/troubleshooting/hmscode/0300_0100_0001_0003"
	if got != want {
		t.Errorf("WikiURL() = %v, want %v", got, want)
	}
}
