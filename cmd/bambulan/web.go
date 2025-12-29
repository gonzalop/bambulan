package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/pkg/bambu3mf"
)

//go:embed templates/*
var templateFS embed.FS

type WebCmd struct {
	Bind     string `help:"Address to bind to" default:"127.0.0.1:8080"`
	Secret   string `help:"Secret for session encryption (optional, random default)"`
	CertFile string `help:"TLS certificate file (enables HTTPS)"`
	KeyFile  string `help:"TLS private key file (enables HTTPS)"`
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
}

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
	ClientsMu     sync.Mutex
	Key           []byte
	UseTLS        bool
	CertFile      string
	KeyFile       string
}

func NewWebServer() *WebServer {
	return &WebServer{
		Sessions:      make(map[string]*Session),
		ActiveClients: make(map[string]*bambulan.Client),
	}
}

func (c *WebCmd) Run(ctx *Context) error {
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
	return s.Start()
}

func (s *WebServer) Start() error {
	http.HandleFunc("/", s.handleIndex)
	http.HandleFunc("/files", s.handleFiles)
	http.HandleFunc("/login", s.handleLogin)
	http.HandleFunc("/logout", s.handleLogout)
	http.HandleFunc("/style.css", s.handleStyle)
	http.HandleFunc("/api/status", s.handleAPIStatus)
	http.HandleFunc("/api/control", s.handleAPIControl)
	http.HandleFunc("/api/files", s.handleAPIFiles)
	http.HandleFunc("/api/download", s.handleAPIDownload)
	http.HandleFunc("/api/upload", s.handleAPIUpload)
	http.HandleFunc("/api/delete", s.handleAPIDelete)
	http.HandleFunc("/api/rename", s.handleAPIRename)
	http.HandleFunc("/api/mkdir", s.handleAPIMkdir)
	http.HandleFunc("/api/print", s.handleAPIPrint)
	http.HandleFunc("/api/filament", s.handleAPIFilament)
	http.HandleFunc("/api/thumbnail", s.handleAPIThumbnail)
	http.HandleFunc("/camera", s.handleCamera)

	if s.UseTLS {
		slog.Info("Starting web server with TLS", "addr", s.BindAddr, "cert", s.CertFile)
		return http.ListenAndServeTLS(s.BindAddr, s.CertFile, s.KeyFile, nil)
	}

	slog.Debug("Starting web server", "addr", s.BindAddr)
	return http.ListenAndServe(s.BindAddr, nil)
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

	if exists {
		// Verify host hasn't changed (e.g. DHCP)
		// Verify host hasn't changed (e.g. DHCP)
		if client.MQTT.Hostname != host {
			slog.Info("Host IP changed for serial, reconnecting", "serial", serial, "old_host", client.MQTT.Hostname, "new_host", host)
			client.Stop()
			exists = false
		}
	}

	if !exists {
		slog.Debug("Creating new client connection", "host", host, "serial", serial)

		onUpdate := func(status *bambulan.PrinterStatus) {}

		client = bambulan.NewClient(host, code, serial, onUpdate)
		if err := client.Start(); err != nil {
			return nil, err
		}
		s.ActiveClients[key] = client
	}

	return client, nil
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

func (s *WebServer) handleStyle(w http.ResponseWriter, r *http.Request) {
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
			data.Error = fmt.Sprintf("Failed to connect: %v. Check IP and ensure printer is on.", err)
			renderLogin()
			return
		}

		session.Client = client
		session.Status = client.GetPrinterStatus() // Shared Status pointer

		s.Mu.Lock()
		s.Sessions[sessionID] = session
		s.Mu.Unlock()

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
		if session, exists := s.Sessions[sessionID]; exists {
			session.Client.Stop()
			delete(s.Sessions, sessionID)
		}
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
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *WebServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	session.Mu.RLock()
	defer session.Mu.RUnlock()

	// Create JSON response
	w.Header().Set("Content-Type", "application/json")

	// Detect state transitions and clear metadata when print finishes/stops
	currentState := session.Status.GcodeState
	previousState := session.PreviousGcodeState

	// Active states that can have metadata
	activeStates := map[string]bool{
		"RUNNING": true,
		"PAUSE":   true,
		"PREPARE": true,
	}

	// Terminal states that should clear metadata
	terminalStates := map[string]bool{
		"FINISH": true,
		"FAILED": true,
		"IDLE":   true,
	}

	// If we transitioned from an active state to a terminal state, clear metadata
	if activeStates[previousState] && terminalStates[currentState] {
		slog.Debug("Print state transition detected, clearing metadata", "from", previousState, "to", currentState)
		session.CurrentMetadata = nil
		session.CurrentMetadataFilename = ""
	}

	// Update previous state for next call
	session.PreviousGcodeState = currentState

	// Determine active file to ensure CurrentMetadata is up to date
	activeFile := session.Status.SubtaskName
	if activeFile == "" {
		activeFile = session.Status.GcodeFile
	}
	// Sanitize path (sometimes comes with /)
	if len(activeFile) > 0 && activeFile[0] == '/' {
		activeFile = activeFile[1:]
	}

	// Update CurrentMetadata from cache if needed
	if activeFile != "" && activeFile != "???" {
		// Only fetch/update if printer is actually printing/paused OR if we have nothing shown yet.
		// This prevents overwriting the "Upload Preview" metadata (set during upload) with the old file's metadata
		// when the printer is still IDLE.
		allowedStates := map[string]bool{
			"RUNNING": true,
			"PAUSE":   true,
			"PREPARE": true, // Just in case
		}
		isActive := allowedStates[session.Status.GcodeState]

		if isActive || session.CurrentMetadata == nil {
			var foundMD *bambu3mf.Metadata
			var foundFilename string

			// 1. Try sanitized filename first (if starts with _)
			if strings.HasPrefix(activeFile, "_") {
				sanitized := strings.TrimPrefix(activeFile, "_")
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
				// Trigger fetch if not found and looks like a 3mf/gcode file
				if strings.HasSuffix(strings.ToLower(activeFile), ".3mf") || strings.HasSuffix(strings.ToLower(activeFile), ".gcode.3mf") {
					// We don't want to spam fetch, so ideally valid check done inside fetchMetadataAsync
					// Must run in goroutine to avoid deadlock (fetchMetadataAsync needs Lock, we hold RLock)
					go s.fetchMetadataAsync(session, activeFile)
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
	}

	if session.CurrentMetadata != nil {
		mdMap := map[string]interface{}{
			"plates":    session.CurrentMetadata.Plates,
			"filaments": session.CurrentMetadata.Filaments,
			// Add unified filename field for thumbnails
			"filename": session.CurrentMetadataFilename,
		}
		resp["metadata"] = mdMap
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("JSON encode failed", "error", err)
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

	// 1. Delete file
	if err := session.Client.File.Delete(file); err != nil {
		slog.Error("Delete failed", "path", file, "error", err)
		http.Error(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Debug("Deleted file", "path", file)
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

	if err := session.Client.File.Rename(oldPath, newPath); err != nil {
		slog.Error("Rename failed", "old", oldPath, "new", newPath, "error", err)
		http.Error(w, "Rename failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Debug("Renamed file", "old", oldPath, "new", newPath)
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

	path := r.FormValue("path")
	if path == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	if err := session.Client.File.MakeDirectory(path); err != nil {
		slog.Error("Mkdir failed", "path", path, "error", err)
		http.Error(w, "Mkdir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Debug("Created directory", "path", path)
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
		_, err = session.Client.MQTT.SetChamberLight(true)
	case "light_off":
		_, err = session.Client.MQTT.SetChamberLight(false)
	case "speed_silent":
		_, err = session.Client.MQTT.SetSpeedProfile(bambulan.SpeedSilent)
	case "speed_std":
		_, err = session.Client.MQTT.SetSpeedProfile(bambulan.SpeedStandard)
	case "speed_sport":
		_, err = session.Client.MQTT.SetSpeedProfile(bambulan.SpeedSport)
	case "speed_ludi":
		_, err = session.Client.MQTT.SetSpeedProfile(bambulan.SpeedLudicrous)
	case "pause":
		_, err = session.Client.MQTT.PausePrint()
	case "resume":
		_, err = session.Client.MQTT.ResumePrint()
	case "stop":
		_, err = session.Client.MQTT.StopPrint()
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

	files, err := session.Client.File.ListFiles(dir)
	if err != nil {
		slog.Error("Failed to list files", "dir", dir, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(files); err != nil {
		slog.Error("JSON encode failed", "error", err)
	}
}

func (s *WebServer) handleAPIDownload(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Missing path", http.StatusBadRequest)
		return
	}

	reader, err := session.Client.File.Download(path)
	if err != nil {
		slog.Error("Failed to start download", "path", path, "error", err)
		http.Error(w, "Failed to download", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(path)))
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

	path := r.FormValue("path")
	if path == "" {
		path = "/"
	}
	remotePath := filepath.Join(path, header.Filename)

	// Setup progress tracking
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

	if err := session.Client.File.Upload(remotePath, file, progressCb); err != nil {
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
		remotePath = filepath.Join("/", header.Filename) // Root dir

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

		if err := session.Client.File.Upload(remotePath, file, progressCb); err != nil {
			slog.Error("Upload failed during print start", "path", remotePath, "error", err)
			http.Error(w, "Upload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Check for existing path
		remotePath = r.FormValue("existing_path")
		if remotePath == "" {
			http.Error(w, "Missing file or existing_path", http.StatusBadRequest)
			return
		}
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
	if _, err := session.Client.MQTT.StartPrint(remotePath, opts); err != nil {
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

		if _, err := session.Client.MQTT.LoadFilament(target); err != nil {
			slog.Error("Load filament failed", "target", target, "error", err)
			http.Error(w, "Load filament failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

	case "unload":
		if _, err := session.Client.MQTT.UnloadFilament(); err != nil {
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

	// Start stream callback
	stopChan := make(chan struct{})
	defer close(stopChan)

	onImage := func(frame []byte) {
		// This callback is executed in a separate goroutine.
		// TODO: Implement broadcasting to support multiple simultaneous viewers.

		select {
		case <-stopChan:
			return
		default:
			// Construct MJPEG frame
			header := fmt.Sprintf("\r\n--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
			_, _ = w.Write([]byte(header))
			_, _ = w.Write(frame)
			_, _ = w.Write([]byte("\r\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}

	if err := session.Client.Camera.StartStream(onImage); err != nil {
		slog.Error("Failed to start camera stream", "error", err)
		http.Error(w, "Camera busy", http.StatusServiceUnavailable)
		return
	}
	defer session.Client.Camera.StopStream()

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
		defer func() {
			session.Mu.Lock()
			delete(session.Fetching, filename)
			// Clear download status when done
			session.MetadataDownloadStatus = MetadataDownloadStatus{}
			session.Mu.Unlock()
		}()

		slog.Info("Async fetching metadata for", "file", filename)

		tmpFile, size, tryFile, err := s.downloadMetadataWithRetry(session, filename)
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
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
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

	s.Mu.Lock()
	s.Sessions[sessionID] = session
	s.Mu.Unlock()

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

func (s *WebServer) downloadMetadataWithRetry(session *Session, filename string) (*os.File, int64, string, error) {
	// If the file starts with "_", try the version without it first.
	// The printer often reports "_foo.3mf" but the actual file we want is often "foo.3mf".
	if strings.HasPrefix(filename, "_") {
		corrected := strings.TrimPrefix(filename, "_")
		slog.Debug("Attempting to download sanitized filename first", "original", filename, "try", corrected)
		t, sz, err := s.downloadToTemp(session, corrected)
		if err == nil {
			return t, sz, corrected, nil
		}
		slog.Warn("Sanitized filename not found, falling back to original", "file", corrected, "error", err)
	}

	// Fallback to original filename
	tmpFile, size, err := s.downloadToTemp(session, filename)
	if err != nil {
		slog.Warn("Failed to download metadata", "file", filename, "error", err)
		return nil, 0, "", err
	}
	return tmpFile, size, filename, nil
}

func (s *WebServer) downloadToTemp(session *Session, target string) (*os.File, int64, error) {
	rc, err := session.Client.File.Download(target)
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

	caps := bambulan.GetPrinterCapabilities(session.Status.DeviceModel)
	if fan == "chamber" && !caps.HasChamberFan {
		http.Error(w, "Chamber fan not supported", http.StatusBadRequest)
		return
	}
	if fan == "aux" && !caps.HasAuxFan {
		http.Error(w, "Aux fan not supported", http.StatusBadRequest)
		return
	}

	if _, err := session.Client.MQTT.SetFanSpeed(fan, speed); err != nil {
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

	caps := bambulan.GetPrinterCapabilities(session.Status.DeviceModel)

	var err error
	switch target {
	case "nozzle":
		if temp > caps.MaxNozzleTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxNozzleTemp), http.StatusBadRequest)
			return
		}
		_, err = session.Client.MQTT.SetNozzleTemperature(temp)
	case "bed":
		if temp > caps.MaxBedTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxBedTemp), http.StatusBadRequest)
			return
		}
		_, err = session.Client.MQTT.SetBedTemperature(temp)
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
