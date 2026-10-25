package main

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/gonzalop/bambulan"
)

func TestHandleLogout(t *testing.T) {
	s := NewWebServer()
	serial := "01S00A12345678"

	// Create two sessions for the same printer
	c1 := &bambulan.Client{
		MQTT:   &bambulan.MQTTClient{Serial: serial},
		Camera: &bambulan.CameraClient{},
		File:   &bambulan.FileClient{},
	}
	c2 := &bambulan.Client{
		MQTT:   &bambulan.MQTTClient{Serial: serial},
		Camera: &bambulan.CameraClient{},
		File:   &bambulan.FileClient{},
	}

	s.Sessions["session1"] = &Session{Client: c1}
	s.Sessions["session2"] = &Session{Client: c2}
	s.ActiveClients[serial] = c1 // In reality they would share the same client instance from getClient

	// Mock logout for session1
	req1 := httptest.NewRequest("GET", "/logout", nil)
	req1.AddCookie(&http.Cookie{Name: "bambulan_session", Value: "session1"})
	w1 := httptest.NewRecorder()

	s.handleLogout(w1, req1)

	s.Mu.RLock()
	if _, ok := s.Sessions["session1"]; ok {
		t.Error("Session 1 should have been deleted")
	}
	if _, ok := s.Sessions["session2"]; !ok {
		t.Error("Session 2 should still exist")
	}
	s.Mu.RUnlock()

	s.ClientsMu.Lock()
	if _, ok := s.ActiveClients[serial]; !ok {
		t.Error("Active client should still exist because session 2 is using it")
	}
	s.ClientsMu.Unlock()

	// Mock logout for session2
	req2 := httptest.NewRequest("GET", "/logout", nil)
	req2.AddCookie(&http.Cookie{Name: "bambulan_session", Value: "session2"})
	w2 := httptest.NewRecorder()

	s.handleLogout(w2, req2)

	s.Mu.RLock()
	if _, ok := s.Sessions["session2"]; ok {
		t.Error("Session 2 should have been deleted")
	}
	s.Mu.RUnlock()

	s.ClientsMu.Lock()
	if _, ok := s.ActiveClients[serial]; ok {
		t.Error("Active client should have been deleted because no sessions are using it")
	}
	s.ClientsMu.Unlock()
}

func TestPathSanitization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"../../../etc/passwd", "/etc/passwd"},
		{"/valid/path", "/valid/path"},
		{"valid/path", "/valid/path"},
		{"/../../invalid", "/invalid"},
		{"..", "/"},
		{"./foo/../bar", "/bar"},
	}

	for _, tt := range tests {
		// This simulates path.Clean("/" + input) as used in our handlers
		result := path.Clean("/" + tt.input)
		if result != tt.expected {
			t.Errorf("Sanitization failed for %q: got %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestHandleLogout_RevokesAuthCookie(t *testing.T) {
	s := NewWebServer()
	req := httptest.NewRequest("GET", "/logout", nil)
	w := httptest.NewRecorder()

	s.handleLogout(w, req)

	cookies := w.Result().Cookies()
	var authCleared, sessionCleared bool
	for _, c := range cookies {
		if c.Name == "bambulan_auth" && c.MaxAge < 0 {
			authCleared = true
		}
		if c.Name == "bambulan_session" && c.MaxAge < 0 {
			sessionCleared = true
		}
	}

	if !authCleared {
		t.Fatal("expected bambulan_auth cookie to be cleared with MaxAge < 0")
	}
	if !sessionCleared {
		t.Fatal("expected bambulan_session cookie to be cleared with MaxAge < 0")
	}
}

func TestUploadFilename_TraversalSanitization(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		expectBase string
	}{
		{"normal", "benchy.gcode", "benchy.gcode"},
		{"unix traversal", "../../etc/passwd", "passwd"},
		{"windows traversal", `..\..\windows\system32\calc.exe`, "calc.exe"},
		{"nested traversal", `folder/../../../secret.3mf`, "secret.3mf"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := path.Base(strings.ReplaceAll(tc.filename, "\\", "/"))
			if got != tc.expectBase {
				t.Errorf("path.Base(%q) = %q, want %q", tc.filename, got, tc.expectBase)
			}
		})
	}
}

func TestSessionID_RandomEntropy(t *testing.T) {
	id1 := generateRandomString(32)
	id2 := generateRandomString(32)

	if len(id1) != 64 || len(id2) != 64 {
		t.Fatalf("expected 64 character hex session IDs, got len=%d and len=%d", len(id1), len(id2))
	}
	if id1 == id2 {
		t.Fatalf("expected cryptographically random unique session IDs, got duplicate %s", id1)
	}
}
