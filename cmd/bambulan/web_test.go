package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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

func newTestClient(host, code, serial string) *bambulan.Client {
	return &bambulan.Client{
		MQTT:   &bambulan.MQTTClient{Hostname: host, AccessCode: code, Serial: serial},
		Camera: &bambulan.CameraClient{},
		File:   &bambulan.FileClient{},
	}
}

// A pooled client must never be handed to someone with the wrong access code.
func TestGetClientChecksAccessCode(t *testing.T) {
	const serial, host = "SER1", "127.0.0.1"
	// A live connection proves its code is valid, so a different code is refused
	// without touching the network. No case here dials out.
	orig := isConnected
	isConnected = func(*bambulan.Client) bool { return true }
	t.Cleanup(func() { isConnected = orig })

	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{"matching code reuses client", "good", false},
		{"wrong code is rejected", "junk", true},
		{"empty code is rejected", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewWebServer()
			existing := newTestClient(host, "good", serial)
			s.ActiveClients[serial] = existing

			got, err := s.getClient(host, tt.code, serial)
			if tt.wantErr {
				if !errors.Is(err, errAccessCodeMismatch) {
					t.Fatalf("getClient(%q) error = %v, want errAccessCodeMismatch", tt.code, err)
				}
			} else if err != nil || got != existing {
				t.Fatalf("getClient(%q) = %p, %v; want existing client", tt.code, got, err)
			}
			if s.ActiveClients[serial] != existing {
				t.Error("pooled client was replaced")
			}
		})
	}
}

func TestExpireSessions(t *testing.T) {
	s := NewWebServer()
	now := time.Now()
	stale := &Session{Client: newTestClient("h", "c", "OLD")}
	stale.lastSeen.Store(now.Add(-sessionIdleTimeout - time.Minute).Unix())
	fresh := &Session{Client: newTestClient("h", "c", "NEW")}
	fresh.lastSeen.Store(now.Unix())
	s.Sessions["stale"], s.Sessions["fresh"] = stale, fresh
	s.ActiveClients["OLD"], s.ActiveClients["NEW"] = stale.Client, fresh.Client

	s.expireSessions(now)

	if _, ok := s.Sessions["stale"]; ok {
		t.Error("stale session not expired")
	}
	if _, ok := s.ActiveClients["OLD"]; ok {
		t.Error("client of expired session not stopped")
	}
	if _, ok := s.Sessions["fresh"]; !ok {
		t.Error("fresh session expired")
	}
	if _, ok := s.ActiveClients["NEW"]; !ok {
		t.Error("client of fresh session stopped")
	}
}

func TestAllowLogin(t *testing.T) {
	s := NewWebServer()
	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "10.0.0.9:5555"
	for i := 1; i <= loginRateMax; i++ {
		if !s.allowLogin(req) {
			t.Fatalf("attempt %d blocked, limit is %d", i, loginRateMax)
		}
	}
	if s.allowLogin(req) {
		t.Fatal("attempt over the limit allowed")
	}
	other := httptest.NewRequest("POST", "/login", nil)
	other.RemoteAddr = "10.0.0.10:5555"
	if !s.allowLogin(other) {
		t.Fatal("limit leaked to another IP")
	}
}

func TestMetadataCandidates(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"foo.3mf", []string{"foo.3mf"}},
		{"_foo.3mf", []string{"foo.3mf", "_foo.3mf"}},
		{"job", []string{"job", "job.gcode.3mf", "job.3mf"}},
		{"_job", []string{"job", "_job", "_job.gcode.3mf", "_job.3mf"}},
	}
	for _, tt := range tests {
		if got := metadataCandidates(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("metadataCandidates(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Run with -race: control handlers read the model while the SSE loop swaps Status.
func TestControlHandlersStatusRace(_ *testing.T) {
	s := NewWebServer()
	session := &Session{Status: &bambulan.PrinterStatus{DeviceModel: "C12"}, Client: newTestClient("h", "c", "S")}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			session.Mu.Lock()
			session.Status = &bambulan.PrinterStatus{DeviceModel: "C13"}
			session.Mu.Unlock()
		}
	}()
	for i := 0; i < 200; i++ {
		// Invalid target/fan values return before any MQTT command is sent.
		s.handleTempControl(httptest.NewRecorder(), httptest.NewRequest("POST", "/?target=x&temp=1", nil), session)
		s.handleFanControl(httptest.NewRecorder(), httptest.NewRequest("POST", "/?fan=chamber&speed=1", nil), session)
	}
	<-done
}

func TestAddSessionEvictsLeastRecentlyUsed(t *testing.T) {
	s := NewWebServer()
	now := time.Now().Unix()
	for i := 0; i < maxSessions; i++ {
		sess := &Session{Client: newTestClient("h", "c", "S")}
		sess.lastSeen.Store(now)
		s.Sessions[strconv.Itoa(i)] = sess
	}
	s.Sessions["7"].lastSeen.Store(now - 3600)

	s.addSession("new", &Session{Client: newTestClient("h", "c", "S")})

	if len(s.Sessions) != maxSessions {
		t.Fatalf("len(Sessions) = %d, want %d", len(s.Sessions), maxSessions)
	}
	if _, ok := s.Sessions["7"]; ok {
		t.Error("least recently used session was not evicted")
	}
	if _, ok := s.Sessions["new"]; !ok {
		t.Error("new session was not added")
	}
}

// Delete is recursive, so it must never reach the printer for the root folder.
func TestHandleAPIDeleteRefusesRoot(t *testing.T) {
	s := NewWebServer()
	s.Sessions["sid"] = &Session{CSRFToken: "tok", Client: newTestClient("h", "c", "S")}
	for _, file := range []string{"/", "../..", "/./"} {
		req := httptest.NewRequest("POST", "/api/b/delete", strings.NewReader("file="+url.QueryEscape(file)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", "tok")
		req.AddCookie(&http.Cookie{Name: "bambulan_session", Value: "sid"})
		rr := httptest.NewRecorder()
		s.handleAPIDelete(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("delete %q: status %d, want 400", file, rr.Code)
		}
	}
}
