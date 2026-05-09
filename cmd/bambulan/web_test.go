package main

import (
	"net/http"
	"net/http/httptest"
	"path"
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
