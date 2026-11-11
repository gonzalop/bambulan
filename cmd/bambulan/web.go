package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/homeassistant"
	"github.com/gonzalop/bambulan/internal/bambu3mf"
)

//go:embed templates/*
var templateFS embed.FS

type WebCmd struct {
	Bind        string   `help:"Address to bind to" default:"127.0.0.1:8080"`
	Secret      string   `help:"Secret for session encryption (optional, random default)" env:"BAMBULAN_SECRET"`
	CertFile    string   `help:"TLS certificate file (enables HTTPS)"`
	KeyFile     string   `help:"TLS private key file (enables HTTPS)"`
	MaxFileSize ByteSize `help:"Maximum allowed size for 3MF file entries" default:"50MB"`

	// HA MQTT Flags
	MQTTBroker   string `help:"MQTT broker address for Home Assistant (e.g. tcp://192.168.1.100:1883)" env:"BAMBULAN_MQTT_BROKER" name:"mqtt-broker"`
	MQTTUser     string `help:"MQTT username" env:"BAMBULAN_MQTT_USER" name:"mqtt-user"`
	MQTTPassword string `help:"MQTT password" env:"BAMBULAN_MQTT_PASSWORD" name:"mqtt-password"`
	MQTTPrefix   string `help:"MQTT topic prefix for Home Assistant discovery" env:"BAMBULAN_MQTT_PREFIX" default:"homeassistant" name:"mqtt-prefix"`
}

type Session struct {
	Client                  *bambulan.Client
	Status                  *bambulan.PrinterStatus
	CSRFToken               string
	UploadStatus            UploadStatus
	MetadataDownloadStatus  MetadataDownloadStatus
	CurrentMetadata         *bambu3mf.Metadata
	CurrentMetadataFilename string // Track which file the metadata belongs to
	MetadataCache           map[string]*bambu3mf.Metadata
	ThumbnailCache          map[string][]byte
	FailedFetches           map[string]int  // Track retry counts
	Fetching                map[string]bool // Track active fetches to prevent thundering herd // key: filename/plate_id
	PreviousGcodeState      string          // Track previous state to detect transitions
	Mu                      sync.RWMutex

	lastSeen atomic.Int64 // Unix seconds of the last request or live update
}

func (s *Session) touch() { s.lastSeen.Store(time.Now().Unix()) }

const (
	sessionIdleTimeout = 24 * time.Hour
	maxSessions        = 256
	loginRateWindow    = time.Minute
	loginRateMax       = 10 // login attempts per client IP per window
)

type UploadStatus struct {
	Uploading bool    `json:"uploading"`
	Filename  string  `json:"filename"`
	Current   int64   `json:"current"`
	Total     int64   `json:"total"`
	Percent   float64 `json:"percent"`
}

type MetadataDownloadStatus struct {
	Downloading bool    `json:"downloading"`
	Filename    string  `json:"filename"`
	Current     int64   `json:"current"`
	Total       int64   `json:"total"`
	Percent     float64 `json:"percent"`
}

type WebServer struct {
	BindAddr string
	Sessions map[string]*Session
	Mu       sync.RWMutex
	// Connection pooling
	ActiveClients map[string]*bambulan.Client
	ActiveBridges map[string]context.CancelFunc
	ClientsMu     sync.Mutex
	Key           []byte
	UseTLS        bool
	CertFile      string
	KeyFile       string
	MaxFileSize   int64

	// HA MQTT Bridge
	MQTTBroker   string
	MQTTUser     string
	MQTTPassword string
	MQTTPrefix   string

	loginMu       sync.Mutex
	loginAttempts map[string]*loginWindow
}

type loginWindow struct {
	start time.Time
	count int
}

// allowLogin reports whether the client IP is under the login rate limit.
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

// addSession stores a new session. When the limit is reached it evicts the
// least recently used one: real users keep their sessions active, so unused
// sessions created just to fill the table go first.
func (s *WebServer) addSession(id string, session *Session) {
	session.touch()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.Sessions) >= maxSessions {
		var oldestID string
		var oldest int64
		for sid, sess := range s.Sessions {
			if seen := sess.lastSeen.Load(); oldestID == "" || seen < oldest {
				oldestID, oldest = sid, seen
			}
		}
		s.removeSessionLocked(oldestID)
	}
	s.Sessions[id] = session
}

// removeSessionLocked deletes a session and stops its printer client if no
// other session uses it. s.Mu must be held.
func (s *WebServer) removeSessionLocked(id string) {
	session, ok := s.Sessions[id]
	if !ok {
		return
	}
	delete(s.Sessions, id)
	if session.Client == nil {
		return
	}
	serial := session.Client.MQTT.Serial
	for _, sess := range s.Sessions {
		if sess.Client != nil && sess.Client.MQTT.Serial == serial {
			return
		}
	}
	s.ClientsMu.Lock()
	s.stopClientLocked(serial)
	s.ClientsMu.Unlock()
}

// expireSessions removes sessions idle for longer than sessionIdleTimeout,
// and forgets old login rate-limit windows.
func (s *WebServer) expireSessions(now time.Time) {
	cutoff := now.Add(-sessionIdleTimeout).Unix()
	s.Mu.Lock()
	for id, session := range s.Sessions {
		if session.lastSeen.Load() < cutoff {
			slog.Debug("Expiring idle session")
			s.removeSessionLocked(id)
		}
	}
	s.Mu.Unlock()

	s.loginMu.Lock()
	for ip, w := range s.loginAttempts {
		if now.Sub(w.start) > loginRateWindow {
			delete(s.loginAttempts, ip)
		}
	}
	s.loginMu.Unlock()
}

func NewWebServer() *WebServer {
	return &WebServer{
		Sessions:      make(map[string]*Session),
		ActiveClients: make(map[string]*bambulan.Client),
		ActiveBridges: make(map[string]context.CancelFunc),
		loginAttempts: make(map[string]*loginWindow),
		MaxFileSize:   bambu3mf.DefaultMaxFileSize,
	}
}

// Run starts the web dashboard server.
func (c *WebCmd) Run(_ *Context) error {
	// Validate TLS configuration
	if (c.CertFile != "" && c.KeyFile == "") || (c.CertFile == "" && c.KeyFile != "") {
		return fmt.Errorf("both --cert and --key must be provided together for HTTPS")
	}

	secret := c.Secret
	if secret == "" {
		// Generate 32 bytes of random data
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("failed to generate random secret: %w", err)
		}
		// Encode to base64 to make it printable and compact
		secret = base64.StdEncoding.EncodeToString(b)
		fmt.Printf("No secret provided. Generated session secret:\n\n\t%s\n\n", secret)
		fmt.Println("To restore sessions after a restart, use this secret:")
		fmt.Printf("\tbambulan web --secret \"%s\"\n\n", secret)
	}

	// Derive 32-byte key from secret using SHA-256
	keyHash := sha256.Sum256([]byte(secret))
	key := keyHash[:]

	s := NewWebServer()
	s.BindAddr = c.Bind
	s.Key = key
	s.UseTLS = c.CertFile != "" && c.KeyFile != ""
	s.CertFile = c.CertFile
	s.KeyFile = c.KeyFile
	s.MaxFileSize = int64(c.MaxFileSize)
	s.MQTTBroker = c.MQTTBroker
	s.MQTTUser = c.MQTTUser
	s.MQTTPassword = c.MQTTPassword
	s.MQTTPrefix = c.MQTTPrefix

	return s.Start()
}

func (s *WebServer) Start() error {
	http.HandleFunc("/", s.handleIndex)
	http.HandleFunc("/files", s.handleFiles)
	http.HandleFunc("/login", s.handleLogin)
	http.HandleFunc("/logout", s.handleLogout)
	http.HandleFunc("/style.css", s.handleStyle)
	http.HandleFunc("/api/b/status", s.handleAPIStatus)
	http.HandleFunc("/api/b/events", s.handleAPIEvents)
	http.HandleFunc("/api/b/control", s.handleAPIControl)
	http.HandleFunc("/api/b/files", s.handleAPIFiles)
	http.HandleFunc("/api/b/download", s.handleAPIDownload)
	http.HandleFunc("/api/b/upload", s.handleAPIUpload)
	http.HandleFunc("/api/b/delete", s.handleAPIDelete)
	http.HandleFunc("/api/b/rename", s.handleAPIRename)
	http.HandleFunc("/api/b/mkdir", s.handleAPIMkdir)
	http.HandleFunc("/api/b/print", s.handleAPIPrint)
	http.HandleFunc("/api/b/filament", s.handleAPIFilament)
	http.HandleFunc("/api/b/thumbnail", s.handleAPIThumbnail)
	http.HandleFunc("/camera", s.handleCamera)

	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			s.expireSessions(now)
		}
	}()

	// No ReadTimeout/WriteTimeout: uploads, SSE and the camera stream are long-lived.
	srv := &http.Server{
		Addr:              s.BindAddr,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if s.UseTLS {
		slog.Info("Starting web server with TLS", "addr", s.BindAddr, "cert", s.CertFile)
		return srv.ListenAndServeTLS(s.CertFile, s.KeyFile)
	}

	slog.Debug("Starting web server", "addr", s.BindAddr)
	return srv.ListenAndServe()
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

// getClient returns an existing client or creates a new one.
// It matches clients by Serial number. If Host changes for the same Serial, it reconnects.
func (s *WebServer) getClient(host, code, serial string) (*bambulan.Client, error) {
	s.ClientsMu.Lock()
	defer s.ClientsMu.Unlock()

	key := serial
	client, exists := s.ActiveClients[key]

	if exists && subtle.ConstantTimeCompare([]byte(client.MQTT.AccessCode), []byte(code)) != 1 {
		// Never hand out a live connection to someone who doesn't know its code.
		// A disconnected client may hold a stale code (changed on the printer), so
		// let the new code replace it, but only if it actually connects.
		if isConnected(client) {
			slog.Warn("Login rejected: access code does not match active connection", "serial", serial)
			return nil, errAccessCodeMismatch
		}
		slog.Info("Access code changed for disconnected printer, reconnecting", "serial", serial)
		newClient := bambulan.NewClient(host, code, serial)
		if err := newClient.Start(); err != nil {
			return nil, err
		}
		s.stopClientLocked(key)
		client = newClient
		s.ActiveClients[key] = client
		s.startBridgeLocked(key, client)
		return client, nil
	}

	if exists {
		// Verify host hasn't changed (e.g. DHCP)
		if client.MQTT.Hostname != host {
			slog.Info("Host IP changed for serial, reconnecting", "serial", serial, "old_host", client.MQTT.Hostname, "new_host", host)
			s.stopClientLocked(key)
			exists = false
		}
	}

	if !exists {
		slog.Debug("Creating new client connection", "host", host, "serial", serial)

		client = bambulan.NewClient(host, code, serial)
		if err := client.Start(); err != nil {
			return nil, err
		}
		s.ActiveClients[key] = client
		s.startBridgeLocked(key, client)
	}

	return client, nil
}

var errAccessCodeMismatch = errors.New("access code does not match")

// isConnected is a variable so tests can simulate a live printer connection.
var isConnected = func(c *bambulan.Client) bool { return c.MQTT.IsConnected() }

// startBridgeLocked starts the Home Assistant bridge for a client, if configured.
// s.ClientsMu must be held.
func (s *WebServer) startBridgeLocked(key string, client *bambulan.Client) {
	if s.MQTTBroker == "" {
		return
	}
	serial := client.MQTT.Serial
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	s.ActiveBridges[key] = bridgeCancel
	go func() {
		slog.Info("Starting Home Assistant bridge for newly connected printer", "serial", serial, "broker", s.MQTTBroker)
		bridge, err := homeassistant.NewBridge(client, s.MQTTBroker, s.MQTTUser, s.MQTTPassword, s.MQTTPrefix)
		if err != nil {
			slog.Error("Failed to initialize Home Assistant bridge", "serial", serial, "error", err)
			return
		}
		defer bridge.Close()
		if err := bridge.Start(bridgeCtx); err != nil && bridgeCtx.Err() == nil {
			slog.Error("Home Assistant bridge error", "serial", serial, "error", err)
		}
	}()
}

// stopClientLocked stops the bridge and client for key and removes them.
// s.ClientsMu must be held.
func (s *WebServer) stopClientLocked(key string) {
	if cancelBridge, ok := s.ActiveBridges[key]; ok {
		cancelBridge()
		delete(s.ActiveBridges, key)
	}
	if client, ok := s.ActiveClients[key]; ok {
		client.Stop()
		delete(s.ActiveClients, key)
	}
}

// validateCSRF checks the request for a valid CSRF token against the session.
// It checks the "X-CSRF-Token" header and the "_csrf" form value.
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

	return token == session.CSRFToken
}

func (s *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	session, ok := s.getSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// Render dashboard
	tmpl, err := template.ParseFS(templateFS, "templates/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// We pass the session (which contains Status) to the template
	session.Mu.RLock()
	defer session.Mu.RUnlock()
	data := struct {
		Status       *bambulan.PrinterStatus
		Host         string
		CSRFToken    string
		Capabilities bambulan.PrinterCapability
	}{
		Status:       session.Status,
		Host:         session.Client.MQTT.Hostname,
		CSRFToken:    session.CSRFToken,
		Capabilities: bambulan.GetPrinterCapabilities(session.Status.DeviceModel),
	}
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("Template execution failed", "error", err)
	}
}

func (s *WebServer) handleFiles(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	tmpl, err := template.ParseFS(templateFS, "templates/files.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	session.Mu.RLock()
	defer session.Mu.RUnlock()

	data := struct {
		CSRFToken string
	}{
		CSRFToken: session.CSRFToken,
	}

	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("Template execution failed", "error", err)
	}
}

func (s *WebServer) handleStyle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css")
	content, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		http.Error(w, "CSS not found", http.StatusNotFound)
		return
	}
	if _, err := w.Write(content); err != nil {
		slog.Error("Failed to write CSS", "error", err)
	}
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

		// Create secure session ID
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		sessionID := hex.EncodeToString(b)

		session := &Session{
			Status:         &bambulan.PrinterStatus{},
			CSRFToken:      generateRandomString(32),
			MetadataCache:  make(map[string]*bambu3mf.Metadata),
			ThumbnailCache: make(map[string][]byte),
			FailedFetches:  make(map[string]int),
			Fetching:       make(map[string]bool),
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

		session.Client = client
		session.Status = client.GetPrinterStatus() // Shared Status pointer

		s.addSession(sessionID, session)

		http.SetCookie(w, &http.Cookie{
			Name:     "bambulan_session",
			Value:    sessionID,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.UseTLS,
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
			encrypted, err := encrypt(authBytes, s.Key)
			if err == nil {
				http.SetCookie(w, &http.Cookie{
					Name:     "bambulan_auth",
					Value:    base64.StdEncoding.EncodeToString(encrypted),
					Path:     "/",
					HttpOnly: true,
					Secure:   s.UseTLS,
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
			if decryptedBytes, err := decrypt(encryptedBytes, s.Key); err == nil {
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
		s.Mu.Lock()
		s.removeSessionLocked(sessionID)
		s.Mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "bambulan_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.UseTLS,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "bambulan_auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.UseTLS,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

var (
	activeStates   = map[string]bool{"RUNNING": true, "PAUSE": true, "PREPARE": true}
	terminalStates = map[string]bool{"FINISH": true, "FAILED": true, "IDLE": true}
)

func (s *WebServer) buildStatusPayload(session *Session) map[string]any {
	session.Mu.Lock()

	// Detect state transitions and clear metadata when print finishes/stops
	currentState := session.Status.GcodeState
	previousState := session.PreviousGcodeState

	if activeStates[previousState] && terminalStates[currentState] {
		slog.Debug("Print state transition detected, clearing metadata", "from", previousState, "to", currentState)
		if session.CurrentMetadataFilename != "" {
			delete(session.MetadataCache, session.CurrentMetadataFilename)
			slog.Debug("Cleared metadata cache entry", "file", session.CurrentMetadataFilename)
		}
		session.CurrentMetadata = nil
		session.CurrentMetadataFilename = ""
	}

	session.PreviousGcodeState = currentState

	activeFile := session.Status.SubtaskName
	if activeFile == "" {
		activeFile = session.Status.GcodeFile
	}
	if len(activeFile) > 0 && activeFile[0] == '/' {
		activeFile = activeFile[1:]
	}

	var fetchFile string

	if activeFile != "" && activeFile != "???" {
		isActive := activeStates[session.Status.GcodeState]

		if isActive || session.CurrentMetadata == nil {
			var foundMD *bambu3mf.Metadata
			var foundFilename string

			// 1. Try sanitized filename first (if starts with _)
			if after, ok := strings.CutPrefix(activeFile, "_"); ok {
				sanitized := after
				if md, ok := session.MetadataCache[sanitized]; ok {
					foundMD = md
					foundFilename = sanitized
				}
			}

			// 2. Try exact filename
			if foundMD == nil {
				if md, ok := session.MetadataCache[activeFile]; ok {
					foundMD = md
					foundFilename = activeFile
				}
			}

			if foundMD != nil {
				session.CurrentMetadata = foundMD
				session.CurrentMetadataFilename = foundFilename
			} else if isActive {
				// Trigger fetch if not found and looks like a 3mf/gcode file OR has no extension
				lower := strings.ToLower(activeFile)
				if strings.HasSuffix(lower, ".3mf") || strings.HasSuffix(lower, ".gcode.3mf") || filepath.Ext(activeFile) == "" {
					fetchFile = activeFile
				}
			}
		}
	}

	// Wrap in top-level object as expected by the frontend.
	resp := map[string]any{
		"print":                    session.Status,
		"stage_message":            session.Status.GetPrintStageName(),
		"upload_status":            session.UploadStatus,
		"metadata_download_status": session.MetadataDownloadStatus,
		"capabilities":             bambulan.GetPrinterCapabilities(session.Status.DeviceModel),
		"metadata":                 nil,
		"hms": map[string]any{
			"message":     session.Status.HMSMessage(),
			"description": session.Status.HMSDescription(),
			"urls":        session.Status.WikiURLs(),
			"active":      len(session.Status.Hms) > 0,
		},
	}

	if session.CurrentMetadata != nil {
		mdMap := map[string]any{
			"plates":    session.CurrentMetadata.Plates,
			"filaments": session.CurrentMetadata.Filaments,
			// Add unified filename field for thumbnails
			"filename": session.CurrentMetadataFilename,
		}
		resp["metadata"] = mdMap
	}

	session.Mu.Unlock()

	// Must run in goroutine to avoid deadlock, outside of lock
	if fetchFile != "" {
		go s.fetchMetadataAsync(session, fetchFile)
	}

	return resp
}

func (s *WebServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp := s.buildStatusPayload(session)

	w.Header().Set("Content-Type", "application/json")
	if err := json.MarshalWrite(w, resp); err != nil {
		slog.Error("JSON encode failed", "error", err)
	}
}

func computeDelta(oldMap, newMap map[string]any) map[string]any {
	delta := make(map[string]any)
	for k, v := range newMap {
		oldV, ok := oldMap[k]
		if !ok {
			delta[k] = v
			continue
		}

		// If both are maps, recurse
		newMapV, newIsMap := v.(map[string]any)
		oldMapV, oldIsMap := oldV.(map[string]any)

		if newIsMap && oldIsMap {
			subDelta := computeDelta(oldMapV, newMapV)
			if len(subDelta) > 0 {
				delta[k] = subDelta
			}
			continue
		}

		if !reflect.DeepEqual(oldV, v) {
			delta[k] = v
		}
	}
	return delta
}
func (s *WebServer) sendSSEEvent(w io.Writer, event string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	return err
}

func (s *WebServer) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sub := session.Client.Subscribe()
	defer sub.Cancel()

	// Initial State Hydration
	resp := s.buildStatusPayload(session)
	lastJSON, _ := json.Marshal(resp)
	var lastMap map[string]any
	_ = json.Unmarshal(lastJSON, &lastMap)

	if err := s.sendSSEEvent(w, "status_update", resp); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			session.touch() // an open page keeps its session alive, even with the printer off
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case update := <-sub.C:
			session.touch()
			if update != nil {
				session.Mu.Lock()
				session.Status = update
				session.Mu.Unlock()
			}
			payload := s.buildStatusPayload(session)
			newJSON, _ := json.Marshal(payload)

			if bytes.Equal(newJSON, lastJSON) {
				continue
			}

			// Diff against the JSON form so values compare the same way the browser sees them.
			var newMap map[string]any
			_ = json.Unmarshal(newJSON, &newMap)

			delta := computeDelta(lastMap, newMap)
			if len(delta) > 0 {
				if err := s.sendSSEEvent(w, "status_update", delta); err != nil {
					return
				}
				flusher.Flush()
				lastJSON = newJSON
				lastMap = newMap
			}
		}
	}
}

func (s *WebServer) handleAPIDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// CSRF Check
	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	file := r.FormValue("file")
	if file == "" {
		http.Error(w, "Missing file parameter", http.StatusBadRequest)
		return
	}

	// Sanitize path
	cleanPath := path.Clean("/" + file)
	if cleanPath == "/" {
		http.Error(w, "Refusing to delete the root directory", http.StatusBadRequest)
		return
	}

	// Files are deleted directly; folders are deleted with their contents.
	if err := session.Client.File.RemoveAll(r.Context(), cleanPath); err != nil {
		slog.Error("Delete failed", "path", cleanPath, "error", err)
		http.Error(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Debug("Deleted file", "path", cleanPath)
	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	oldPath := r.FormValue("oldPath")
	newPath := r.FormValue("newPath")
	if oldPath == "" || newPath == "" {
		http.Error(w, "Missing path parameters", http.StatusBadRequest)
		return
	}

	// Sanitize paths
	cleanOld := path.Clean("/" + oldPath)
	cleanNew := path.Clean("/" + newPath)

	if err := session.Client.File.Rename(r.Context(), cleanOld, cleanNew); err != nil {
		slog.Error("Rename failed", "old", cleanOld, "new", cleanNew, "error", err)
		http.Error(w, "Rename failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Debug("Renamed file", "old", cleanOld, "new", cleanNew)
	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	pathParam := r.FormValue("path")
	if pathParam == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	// Sanitize path
	cleanPath := path.Clean("/" + pathParam)

	if err := session.Client.File.MakeDirectory(r.Context(), cleanPath); err != nil {
		slog.Error("Mkdir failed", "path", cleanPath, "error", err)
		http.Error(w, "Mkdir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Debug("Created directory", "path", cleanPath)
	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIControl(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate CSRF
	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	// Parse command
	cmd := r.FormValue("cmd")

	var err error
	switch cmd {
	case "light_on":
		_, err = session.Client.MQTT.SetChamberLight(r.Context(), true)
	case "light_off":
		_, err = session.Client.MQTT.SetChamberLight(r.Context(), false)
	case "speed_silent":
		_, err = session.Client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedSilent)
	case "speed_std":
		_, err = session.Client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedStandard)
	case "speed_sport":
		_, err = session.Client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedSport)
	case "speed_ludi":
		_, err = session.Client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedLudicrous)
	case "pause":
		_, err = session.Client.MQTT.PausePrint(r.Context())
	case "resume":
		_, err = session.Client.MQTT.ResumePrint(r.Context())
	case "stop":
		_, err = session.Client.MQTT.StopPrint(r.Context())
	case "set_fan":
		s.handleFanControl(w, r, session)
		return
	case "set_temp":
		s.handleTempControl(w, r, session)
		return
	default:
		http.Error(w, "Unknown command", http.StatusBadRequest)
		return
	}

	if err != nil {
		slog.Error("Control command failed", "cmd", cmd, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIFiles(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = "/"
	}

	// Sanitize path
	cleanDir := path.Clean("/" + dir)

	files, err := session.Client.File.ListFiles(r.Context(), cleanDir)
	if err != nil {
		slog.Error("Failed to list files", "dir", cleanDir, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.MarshalWrite(w, files); err != nil {
		slog.Error("JSON encode failed", "error", err)
	}
}

func (s *WebServer) handleAPIDownload(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	pathParam := r.URL.Query().Get("path")
	if pathParam == "" {
		http.Error(w, "Missing path", http.StatusBadRequest)
		return
	}

	// Sanitize path
	cleanPath := path.Clean("/" + pathParam)

	reader, err := session.Client.File.Download(r.Context(), cleanPath)
	if err != nil {
		slog.Error("Failed to start download", "path", cleanPath, "error", err)
		http.Error(w, "Failed to download", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", path.Base(cleanPath)))
	w.Header().Set("Content-Type", "application/octet-stream")

	if _, err := io.Copy(w, reader); err != nil {
		slog.Error("Download stream failed", "error", err)
	}
}

func (s *WebServer) handleAPIUpload(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate CSRF
	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	// Limit upload size (e.g., 500MB)
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "File too large or invalid form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	pathParam := r.FormValue("path")
	if pathParam == "" {
		pathParam = "/"
	}
	// Sanitize path and join using path (not filepath) for remote FTP
	cleanDir := path.Clean("/" + pathParam)
	cleanFilename := path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if cleanFilename == "." || cleanFilename == "/" || cleanFilename == ".." || cleanFilename == "" {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	remotePath := path.Join(cleanDir, cleanFilename)

	// Setup progress tracking
	session.Mu.Lock()
	session.UploadStatus = UploadStatus{
		Uploading: true,
		Filename:  cleanFilename,
	}
	session.Mu.Unlock()

	defer func() {
		session.Mu.Lock()
		session.UploadStatus = UploadStatus{} // Reset
		session.Mu.Unlock()
	}()

	progressCb := func(current, total int64) {
		session.Mu.Lock()
		defer session.Mu.Unlock()
		session.UploadStatus.Current = current
		session.UploadStatus.Total = total
		if total > 0 {
			session.UploadStatus.Percent = float64(current) / float64(total) * 100
		}
	}

	if err := session.Client.File.Upload(r.Context(), remotePath, file, progressCb); err != nil {
		slog.Error("Upload failed", "path", remotePath, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIPrint(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate CSRF
	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	// Limit upload size (e.g., 500MB)
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "File too large or invalid form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	var remotePath string

	if err == nil {
		defer file.Close()

		// Attempt to parse 3MF metadata if it's a 3mf file
		s.processUploaded3MF(session, file, header)

		// Reset file position after reading
		if seeker, ok := file.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				slog.Error("Failed to reset file pointer", "error", err)
				http.Error(w, "Internal processing error", http.StatusInternalServerError)
				return
			}
		}

		// 1. Upload file
		remotePath = path.Join("/", header.Filename) // Root dir

		session.Mu.Lock()
		session.UploadStatus = UploadStatus{
			Uploading: true,
			Filename:  header.Filename,
		}
		session.Mu.Unlock()

		defer func() {
			session.Mu.Lock()
			session.UploadStatus = UploadStatus{} // Reset
			session.Mu.Unlock()
		}()

		progressCb := func(current, total int64) {
			session.Mu.Lock()
			defer session.Mu.Unlock()
			session.UploadStatus.Current = current
			session.UploadStatus.Total = total
			if total > 0 {
				session.UploadStatus.Percent = float64(current) / float64(total) * 100
			}
		}

		if err := session.Client.File.Upload(r.Context(), remotePath, file, progressCb); err != nil {
			slog.Error("Upload failed during print start", "path", remotePath, "error", err)
			http.Error(w, "Upload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Check for existing path
		existingPath := r.FormValue("existing_path")
		if existingPath == "" {
			http.Error(w, "Missing file or existing_path", http.StatusBadRequest)
			return
		}
		// Sanitize existing path
		remotePath = path.Clean("/" + existingPath)
		// No upload needed
	}

	// 2. Parse options
	parseBool := func(v string) bool {
		return v == "true" || v == "on" || v == "1"
	}

	opts := bambulan.PrintOptions{
		BedType:              r.FormValue("bed_type"),
		Timelapse:            parseBool(r.FormValue("timelapse")),
		BedLeveling:          parseBool(r.FormValue("bed_leveling")),
		FlowCalibration:      parseBool(r.FormValue("flow_calibration")),
		VibrationCalibration: parseBool(r.FormValue("vibration_calibration")),
		LayerInspection:      parseBool(r.FormValue("layer_inspection")),
		UseAMS:               parseBool(r.FormValue("use_ams")),
	}

	// 3. Start Print
	if _, err := session.Client.MQTT.StartPrint(r.Context(), remotePath, opts); err != nil {
		slog.Error("Start print failed", "path", remotePath, "error", err)
		http.Error(w, "Start print failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIFilament(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate CSRF
	if !s.validateCSRF(r, session) {
		http.Error(w, "Invalid CSRF Token", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")

	switch action {
	case "load":
		targetStr := r.FormValue("target")
		if targetStr == "" {
			http.Error(w, "Missing target parameter", http.StatusBadRequest)
			return
		}

		target, err := strconv.Atoi(targetStr)
		if err != nil {
			http.Error(w, "Invalid target value", http.StatusBadRequest)
			return
		}

		if _, err := session.Client.MQTT.LoadFilament(r.Context(), target); err != nil {
			slog.Error("Load filament failed", "target", target, "error", err)
			http.Error(w, "Load filament failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

	case "unload":
		if _, err := session.Client.MQTT.UnloadFilament(r.Context()); err != nil {
			slog.Error("Unload filament failed", "error", err)
			http.Error(w, "Unload filament failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleCamera(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")

	// The stream goroutine may still call onImage after StopStream, so writes
	// are serialized with shutdown: once stopped is set, w is never touched.
	var (
		writeMu sync.Mutex
		stopped bool
	)

	onImage := func(frame []byte) {
		// This callback is executed in a separate goroutine.
		// TODO: Implement broadcasting to support multiple simultaneous viewers.

		writeMu.Lock()
		defer writeMu.Unlock()
		if stopped {
			return
		}
		// Construct MJPEG frame
		header := fmt.Sprintf("\r\n--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
		_, _ = w.Write([]byte(header))
		_, _ = w.Write(frame)
		_, _ = w.Write([]byte("\r\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	if err := session.Client.Camera.StartStream(onImage); err != nil {
		slog.Error("Failed to start camera stream", "error", err)
		http.Error(w, "Camera busy", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		writeMu.Lock()
		stopped = true
		writeMu.Unlock()
		session.Client.Camera.StopStream()
	}()

	// Wait until client disconnects
	<-r.Context().Done()
}

func (s *WebServer) fetchMetadataAsync(session *Session, filename string) {
	// Check concurrency and retry limits
	session.Mu.Lock()
	if session.Fetching[filename] {
		session.Mu.Unlock()
		return
	}
	if session.FailedFetches[filename] >= 3 {
		session.Mu.Unlock()
		return
	}
	session.Fetching[filename] = true
	session.Mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		defer func() {
			session.Mu.Lock()
			delete(session.Fetching, filename)
			// Clear download status when done
			session.MetadataDownloadStatus = MetadataDownloadStatus{}
			session.Mu.Unlock()
		}()

		slog.Info("Async fetching metadata for", "file", filename)

		tmpFile, size, tryFile, err := s.downloadMetadataWithRetry(ctx, session, filename)
		if err != nil {
			slog.Error("Failed to download metadata after retry", "file", filename, "error", err)
			session.Mu.Lock()
			session.FailedFetches[filename]++
			if session.FailedFetches[filename] >= 3 {
				slog.Debug("Skipping metadata fetch, max retries reached", "file", filename, "count", session.FailedFetches[filename])
			}
			session.Mu.Unlock()
			return
		}

		// Cleanup logic for successful/parsed file at end of scope
		defer func() {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}()

		rdr, err := bambu3mf.NewReader(tmpFile, size)
		if err != nil {
			slog.Warn("Failed to open 3mf reader", "error", err)
			return
		}
		rdr.MaxFileSize = s.MaxFileSize
		defer rdr.Close()

		md, err := rdr.ParseMetadata()
		if err != nil {
			slog.Warn("Failed to parse metadata", "error", err)
			return
		}

		s.updateSessionMetadata(session, filename, tryFile, md, rdr)
		slog.Debug("Successfully fetched metadata", "file", tryFile)
	}()
}

func (s *WebServer) handleAPIThumbnail(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "Missing key", http.StatusBadRequest)
		return
	}

	session.Mu.RLock()
	data, exists := session.ThumbnailCache[key]
	session.Mu.RUnlock()

	if !exists {
		http.Error(w, "Thumbnail not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if _, err := w.Write(data); err != nil {
		slog.Error("Failed to write thumbnail", "error", err)
	}
}

// Helpers

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

	s.Mu.RLock()
	session, exists := s.Sessions[id]
	s.Mu.RUnlock()

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
	decryptedBytes, err := decrypt(dataBytes, s.Key)
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

	// Re-create session
	sessionID := generateRandomString(32)
	session := &Session{
		Status:         &bambulan.PrinterStatus{},
		CSRFToken:      generateRandomString(32),
		MetadataCache:  make(map[string]*bambu3mf.Metadata),
		ThumbnailCache: make(map[string][]byte),
		FailedFetches:  make(map[string]int),
		Fetching:       make(map[string]bool),
	}

	client, err := s.getClient(host, code, serial)
	if err != nil {
		slog.Error("Failed to restore session connection", "error", err)
		return nil, false
	}
	session.Client = client
	session.Status = client.GetPrinterStatus()

	s.addSession(sessionID, session)

	// Update the session cookie so subsequent requests use this session
	http.SetCookie(w, &http.Cookie{
		Name:     "bambulan_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.UseTLS,
		SameSite: http.SameSiteStrictMode,
	})

	return session, true
}

func (s *WebServer) processUploaded3MF(session *Session, file multipart.File, header *multipart.FileHeader) {
	if filepath.Ext(header.Filename) != ".3mf" {
		return
	}

	ra, ok := file.(io.ReaderAt)
	if !ok {
		return
	}

	rdr, err := bambu3mf.NewReader(ra, header.Size)
	if err != nil {
		slog.Warn("Failed to create 3MF reader", "error", err)
		return
	}
	rdr.MaxFileSize = s.MaxFileSize
	defer rdr.Close()

	md, err := rdr.ParseMetadata()
	if err != nil {
		slog.Warn("Failed to parse 3MF metadata", "error", err)
		return
	}

	slog.Info("Parsed 3MF metadata", "file", header.Filename, "plates", len(md.Plates))

	session.Mu.Lock()
	defer session.Mu.Unlock()

	session.CurrentMetadata = md
	session.CurrentMetadataFilename = header.Filename
	if session.MetadataCache == nil {
		session.MetadataCache = make(map[string]*bambu3mf.Metadata)
	}
	session.MetadataCache[header.Filename] = md

	// Cache thumbnails
	if session.ThumbnailCache == nil {
		session.ThumbnailCache = make(map[string][]byte)
	}
	for _, p := range md.Plates {
		// Cache small thumbnail
		if p.ThumbnailSmall != "" {
			if small, err := rdr.ReadFile(p.ThumbnailSmall); err == nil {
				// Key format: filename/plate_id_small
				key := fmt.Sprintf("%s/%d_small", header.Filename, p.ID)
				session.ThumbnailCache[key] = small
			}
		}
		// Cache large thumbnail
		if p.ThumbnailPath != "" {
			if large, err := rdr.ReadFile(p.ThumbnailPath); err == nil {
				key := fmt.Sprintf("%s/%d", header.Filename, p.ID)
				session.ThumbnailCache[key] = large
			}
		}
	}
}

// metadataCandidates lists the names to try, in order, when downloading the
// file for a print job name reported by the printer.
func metadataCandidates(filename string) []string {
	var names []string
	// The printer often reports "_foo.3mf" when the actual file is "foo.3mf".
	if after, ok := strings.CutPrefix(filename, "_"); ok {
		names = append(names, after)
	}
	names = append(names, filename)
	// Job names without an extension: .gcode.3mf is the most likely for print jobs.
	if filepath.Ext(filename) == "" {
		names = append(names, filename+".gcode.3mf", filename+".3mf")
	}
	return names
}

func (s *WebServer) downloadMetadataWithRetry(ctx context.Context, session *Session, filename string) (*os.File, int64, string, error) {
	var lastErr error
	for _, name := range metadataCandidates(filename) {
		t, sz, err := s.downloadToTemp(ctx, session, name)
		if err == nil {
			return t, sz, name, nil
		}
		slog.Warn("Metadata download failed", "file", name, "error", err)
		lastErr = err
	}
	return nil, 0, "", lastErr
}

func (s *WebServer) downloadToTemp(ctx context.Context, session *Session, target string) (*os.File, int64, error) {
	rc, err := session.Client.File.Download(ctx, target)
	if err != nil {
		return nil, 0, err
	}
	defer rc.Close()

	tFile, err := os.CreateTemp("", "bambulan_meta_*.3mf")
	if err != nil {
		return nil, 0, err
	}

	success := false
	defer func() {
		if !success {
			os.Remove(tFile.Name())
			tFile.Close()
		}
	}()

	// Set initial download status
	session.Mu.Lock()
	session.MetadataDownloadStatus = MetadataDownloadStatus{
		Downloading: true,
		Filename:    target,
		Current:     0,
		Total:       0,
		Percent:     0,
	}
	session.Mu.Unlock()

	// Create a progress tracking writer
	var written int64
	progressWriter := &progressWriter{
		writer: tFile,
		onProgress: func(n int64) {
			written += n
			session.Mu.Lock()
			session.MetadataDownloadStatus.Current = written
			// We don't know total size for FTP, so just show bytes downloaded
			session.Mu.Unlock()
		},
	}

	sz, err := io.Copy(progressWriter, rc)
	if err != nil {
		return nil, 0, err
	}

	// Update final size
	session.Mu.Lock()
	session.MetadataDownloadStatus.Total = sz
	session.MetadataDownloadStatus.Current = sz
	if sz > 0 {
		session.MetadataDownloadStatus.Percent = 100
	}
	session.Mu.Unlock()

	if _, err := tFile.Seek(0, 0); err != nil {
		return nil, 0, err
	}

	success = true
	return tFile, sz, nil
}

// progressWriter wraps an io.Writer and calls a callback on each write
type progressWriter struct {
	writer     io.Writer
	onProgress func(int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	if n > 0 && pw.onProgress != nil {
		pw.onProgress(int64(n))
	}
	return n, err
}

func (s *WebServer) updateSessionMetadata(session *Session, filename string, tryFile string, md *bambu3mf.Metadata, rdr *bambu3mf.Reader) {
	session.Mu.Lock()
	defer session.Mu.Unlock()

	session.MetadataCache[filename] = md
	if tryFile != filename {
		session.MetadataCache[tryFile] = md
	}

	// Helper to strip leading slash for comparison
	stripSlash := func(s string) string {
		return strings.TrimPrefix(s, "/")
	}

	if stripSlash(session.Status.GcodeFile) == filename || stripSlash(session.Status.SubtaskName) == filename {
		session.CurrentMetadata = md
		session.CurrentMetadataFilename = tryFile
	}

	for _, p := range md.Plates {
		if p.ThumbnailSmall != "" {
			if small, err := rdr.ReadFile(p.ThumbnailSmall); err == nil {
				key := fmt.Sprintf("%s/%d_small", filename, p.ID)
				session.ThumbnailCache[key] = small
				if tryFile != filename {
					key2 := fmt.Sprintf("%s/%d_small", tryFile, p.ID)
					session.ThumbnailCache[key2] = small
				}
			}
		}
		if p.ThumbnailPath != "" {
			if large, err := rdr.ReadFile(p.ThumbnailPath); err == nil {
				key := fmt.Sprintf("%s/%d", filename, p.ID)
				session.ThumbnailCache[key] = large
				if tryFile != filename {
					key2 := fmt.Sprintf("%s/%d", tryFile, p.ID)
					session.ThumbnailCache[key2] = large
				}
			}
		}
	}
}

func (s *WebServer) handleFanControl(w http.ResponseWriter, r *http.Request, session *Session) {
	fan := r.FormValue("fan")
	speedStr := r.FormValue("speed")
	speed, err := strconv.Atoi(speedStr)
	if err != nil {
		http.Error(w, "Invalid speed", http.StatusBadRequest)
		return
	}

	session.Mu.RLock()
	model := session.Status.DeviceModel
	session.Mu.RUnlock()
	caps := bambulan.GetPrinterCapabilities(model)
	if fan == "chamber" && !caps.HasChamberFan {
		http.Error(w, "Chamber fan not supported", http.StatusBadRequest)
		return
	}
	if fan == "aux" && !caps.HasAuxFan {
		http.Error(w, "Aux fan not supported", http.StatusBadRequest)
		return
	}

	if _, err := session.Client.MQTT.SetFanSpeed(r.Context(), fan, speed); err != nil {
		slog.Error("Control command failed", "cmd", "set_fan", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleTempControl(w http.ResponseWriter, r *http.Request, session *Session) {
	target := r.FormValue("target") // nozzle or bed
	tempStr := r.FormValue("temp")
	temp, errAtoi := strconv.Atoi(tempStr)
	if errAtoi != nil {
		http.Error(w, "Invalid temperature", http.StatusBadRequest)
		return
	}

	session.Mu.RLock()
	model := session.Status.DeviceModel
	session.Mu.RUnlock()
	caps := bambulan.GetPrinterCapabilities(model)

	var err error
	switch target {
	case "nozzle":
		if temp > caps.MaxNozzleTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxNozzleTemp), http.StatusBadRequest)
			return
		}
		indexStr := r.FormValue("index")
		index := 0
		if indexStr != "" {
			index, _ = strconv.Atoi(indexStr)
		}
		_, err = session.Client.MQTT.SetNozzleTemperature(r.Context(), temp, index)
	case "bed":
		if temp > caps.MaxBedTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxBedTemp), http.StatusBadRequest)
			return
		}
		_, err = session.Client.MQTT.SetBedTemperature(r.Context(), temp)
	case "chamber":
		if !caps.HasChamberHeater {
			http.Error(w, "Chamber heater not supported", http.StatusBadRequest)
			return
		}
		if temp > caps.MaxChamberTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxChamberTemp), http.StatusBadRequest)
			return
		}
		_, err = session.Client.MQTT.SetChamberTemperature(r.Context(), temp)
	default:
		http.Error(w, "Invalid target type", http.StatusBadRequest)
		return
	}

	if err != nil {
		slog.Error("Control command failed", "cmd", "set_temp", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
