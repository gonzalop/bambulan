package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/internal/bambu3mf"
)

// allowLogin enforces a per-IP rate limit on POST /login.
func (s *WebServer) allowLogin(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	now := time.Now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	w, ok := s.loginAttempts[ip]
	if !ok || now.Sub(w.start) > loginRateWindow {
		s.loginAttempts[ip] = &loginWindow{start: now, count: 1}
		return true
	}
	w.count++
	return w.count <= loginRateMax
}

// addSession registers a new session, evicting the least-recently-used one
// if the table is at capacity.
func (s *WebServer) addSession(id string, session *Session) {
	session.touch()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) >= maxSessions {
		var oldestID string
		var oldest int64
		for sid, sess := range s.sessions {
			if seen := sess.lastSeen.Load(); oldestID == "" || seen < oldest {
				oldestID, oldest = sid, seen
			}
		}
		s.removeSessionLocked(oldestID)
	}
	s.sessions[id] = session
}

// removeSessionLocked deletes id from s.sessions and stops its printer client
// if no other session is using it. s.mu must be held for writing.
func (s *WebServer) removeSessionLocked(id string) {
	session, ok := s.sessions[id]
	if !ok {
		return
	}
	delete(s.sessions, id)
	if session.client == nil {
		return
	}
	serial := session.client.MQTT.Serial
	for _, sess := range s.sessions {
		if sess.client != nil && sess.client.MQTT.Serial == serial {
			return
		}
	}
	s.clientsMu.Lock()
	s.stopClientLocked(serial)
	s.clientsMu.Unlock()
}

// expireSessions drops idle sessions and stale login rate-limit windows.
func (s *WebServer) expireSessions(now time.Time) {
	cutoff := now.Add(-sessionIdleTimeout).Unix()
	s.mu.Lock()
	for id, session := range s.sessions {
		if session.lastSeen.Load() < cutoff {
			slog.Debug("Expiring idle session")
			s.removeSessionLocked(id)
		}
	}
	s.mu.Unlock()

	s.loginMu.Lock()
	for ip, w := range s.loginAttempts {
		if now.Sub(w.start) > loginRateWindow {
			delete(s.loginAttempts, ip)
		}
	}
	s.loginMu.Unlock()
}

func encrypt(data []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data, nil), nil
}

func decrypt(data []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func generateRandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func newSession(client *bambulan.Client) (string, *Session) {
	id := generateRandomString(32)
	if id == "" {
		return "", nil
	}
	return id, &Session{
		client:         client,
		status:         client.PrinterStatus(),
		csrfToken:      generateRandomString(32),
		metadataCache:  make(map[string]*bambu3mf.Metadata),
		thumbnailCache: make(map[string][]byte),
		failedFetches:  make(map[string]int),
		fetching:       make(map[string]bool),
	}
}

// validateCSRF checks if the request contains a valid CSRF token for the session.
func (s *WebServer) validateCSRF(r *http.Request, session *Session) bool {
	// 1. Check Header
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		// 2. Check Form (requires parsing form first, usually done by caller or here)
		// Assuming caller handles form parsing if needed, but for safety we can try FormValue
		// which parses form if not already parsed.
		token = r.FormValue("_csrf")
	}

	if token == "" {
		return false
	}

	return token == session.csrfToken
}

func (s *WebServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Helper struct for template
	type LoginData struct {
		Host   string
		Code   string
		Serial string
		Error  string
	}

	data := LoginData{}

	// Function to render login page
	renderLogin := func() {
		t, err := template.ParseFS(templateFS, "templates/login.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := t.Execute(w, data); err != nil {
			slog.Error("Login template error", "error", err)
		}
	}

	if r.Method == http.MethodPost {
		data.Host = r.FormValue("host")
		data.Code = r.FormValue("code")
		data.Serial = r.FormValue("serial")

		if data.Host == "" || data.Code == "" || data.Serial == "" {
			data.Error = "All fields are required"
			renderLogin()
			return
		}

		if !s.allowLogin(r) {
			http.Error(w, "Too many login attempts, try again later", http.StatusTooManyRequests)
			return
		}

		client, err := s.getClient(data.Host, data.Code, data.Serial)
		if err != nil {
			slog.Error("Connection failed during login", "host", data.Host, "error", err)
			if errors.Is(err, errAccessCodeMismatch) {
				data.Error = "Invalid access code."
			} else {
				data.Error = fmt.Sprintf("Failed to connect: %v. Check IP and ensure printer is on.", err)
			}
			renderLogin()
			return
		}

		sessionID, session := newSession(client)
		if sessionID == "" {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		s.addSession(sessionID, session)

		http.SetCookie(w, &http.Cookie{
			Name:     "bambulan_session",
			Value:    sessionID,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.useTLS,
			SameSite: http.SameSiteStrictMode,
		})

		// Persist auth
		authData := map[string]string{
			"host":   data.Host,
			"code":   data.Code,
			"serial": data.Serial,
		}

		authBytes, err := json.Marshal(authData)
		if err == nil {
			// Encrypt the JSON data
			encrypted, err := encrypt(authBytes, s.key)
			if err == nil {
				http.SetCookie(w, &http.Cookie{
					Name:     "bambulan_auth",
					Value:    base64.StdEncoding.EncodeToString(encrypted),
					Path:     "/",
					HttpOnly: true,
					Secure:   s.useTLS,
					SameSite: http.SameSiteStrictMode,
				})
			} else {
				slog.Error("Failed to encrypt auth cookie", "error", err)
			}
		}

		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// GET request: Try to pre-fill from cookie
	if cookie, err := r.Cookie("bambulan_auth"); err == nil && cookie.Value != "" {
		if encryptedBytes, err := base64.StdEncoding.DecodeString(cookie.Value); err == nil {
			if decryptedBytes, err := decrypt(encryptedBytes, s.key); err == nil {
				var authData map[string]string
				if err := json.Unmarshal(decryptedBytes, &authData); err == nil {
					data.Host = authData["host"]
					data.Code = authData["code"]
					data.Serial = authData["serial"]
				}
			}
		}
	}

	renderLogin()
}

func (s *WebServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sessionID, ok := s.getSessionID(r); ok {
		s.mu.Lock()
		s.removeSessionLocked(sessionID)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "bambulan_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.useTLS,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "bambulan_auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.useTLS,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *WebServer) getSessionID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("bambulan_session")
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

func (s *WebServer) getSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	id, ok := s.getSessionID(r)
	if !ok {
		// No session cookie, check for auth cookie to auto-login
		return s.tryRestoreSession(w, r)
	}

	s.mu.RLock()
	session, exists := s.sessions[id]
	s.mu.RUnlock()

	if exists {
		session.touch()
		return session, true
	}

	// Session ID exists in cookie but not in memory (restart?). Try to restore.
	return s.tryRestoreSession(w, r)
}

func (s *WebServer) tryRestoreSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	cookie, err := r.Cookie("bambulan_auth")
	if err != nil || cookie.Value == "" {
		return nil, false
	}

	dataBytes, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, false
	}

	// Decrypt
	decryptedBytes, err := decrypt(dataBytes, s.key)
	if err != nil {
		slog.Error("Failed to decrypt auth cookie", "error", err)
		return nil, false
	}

	var authData map[string]string
	if err := json.Unmarshal(decryptedBytes, &authData); err != nil {
		return nil, false
	}

	host := authData["host"]
	code := authData["code"]
	serial := authData["serial"]

	if host == "" || code == "" || serial == "" {
		return nil, false
	}

	client, err := s.getClient(host, code, serial)
	if err != nil {
		slog.Error("Failed to restore session connection", "error", err)
		return nil, false
	}

	sessionID, session := newSession(client)
	if sessionID == "" {
		return nil, false
	}
	s.addSession(sessionID, session)

	// Update the session cookie so subsequent requests use this session
	http.SetCookie(w, &http.Cookie{
		Name:     "bambulan_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.useTLS,
		SameSite: http.SameSiteStrictMode,
	})

	return session, true
}
