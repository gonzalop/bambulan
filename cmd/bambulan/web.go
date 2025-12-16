package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gonzalop/bambulan"
)

//go:embed templates/*
var templateFS embed.FS

type WebCmd struct {
	Bind string `help:"Address to bind to" default:":8080"`
}

type Session struct {
	Client *bambulan.Client
	Status *bambulan.PrinterStatus
	Mu     sync.RWMutex
}

type WebServer struct {
	BindAddr string
	Sessions map[string]*Session
	Mu       sync.RWMutex
	// Connection pooling
	ActiveClients map[string]*bambulan.Client
	ClientsMu     sync.Mutex
}

func NewWebServer() *WebServer {
	return &WebServer{
		Sessions:      make(map[string]*Session),
		ActiveClients: make(map[string]*bambulan.Client),
	}
}

func (c *WebCmd) Run(ctx *Context) error {
	s := NewWebServer()
	s.BindAddr = c.Bind
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
	http.HandleFunc("/api/print", s.handleAPIPrint)
	http.HandleFunc("/camera", s.handleCamera)

	slog.Info("Starting web server", "addr", s.BindAddr)
	slog.Info("Web server listening", "url", fmt.Sprintf("http://localhost%s", s.BindAddr))
	return http.ListenAndServe(s.BindAddr, nil)
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
		// Accessing internal MQTT hostname via exposed method would be cleaner,
		// but we know MQTT.Hostname is public.
		if client.MQTT.Hostname != host {
			slog.Info("Host IP changed for serial, reconnecting", "serial", serial, "old_host", client.MQTT.Hostname, "new_host", host)
			client.Stop()
			exists = false
		}
	}

	if !exists {
		slog.Info("Creating new client connection", "host", host, "serial", serial)
		// Status pointer is shared, so per-client callback is not strictly needed for data propagation.
		onUpdate := func(status *bambulan.PrinterStatus) {
			// Optional: Broadcast or log specific events if needed.
		}

		client = bambulan.NewClient(host, code, serial, onUpdate)
		if err := client.Start(); err != nil {
			return nil, err
		}
		s.ActiveClients[key] = client
	}

	return client, nil
}

func (s *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	session, ok := s.getSession(r)
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
		Status *bambulan.PrinterStatus
		Host   string
	}{
		Status: session.Status,
		Host:   session.Client.MQTT.Hostname,
	}
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("Template execution failed", "error", err)
	}
}

func (s *WebServer) handleFiles(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(r)
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
	if err := tmpl.Execute(w, nil); err != nil {
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
			Status: &bambulan.PrinterStatus{},
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
			SameSite: http.SameSiteStrictMode,
		})

		// Persist auth
		authData := map[string]string{
			"host":   data.Host,
			"code":   data.Code,
			"serial": data.Serial,
		}
		if authBytes, err := json.Marshal(authData); err == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     "bambulan_auth",
				Value:    base64.StdEncoding.EncodeToString(authBytes),
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}

		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// GET request: Try to pre-fill from cookie
	if cookie, err := r.Cookie("bambulan_auth"); err == nil && cookie.Value != "" {
		if dataBytes, err := base64.StdEncoding.DecodeString(cookie.Value); err == nil {
			var authData map[string]string
			if err := json.Unmarshal(dataBytes, &authData); err == nil {
				data.Host = authData["host"]
				data.Code = authData["code"]
				data.Serial = authData["serial"]
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
		Name:   "bambulan_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *WebServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	session.Mu.RLock()
	defer session.Mu.RUnlock()

	// Create JSON response
	w.Header().Set("Content-Type", "application/json")

	// Wrap in top-level object as expected by the frontend.
	resp := map[string]interface{}{
		"print":         session.Status,
		"stage_message": session.Status.GetPrintStageName(),
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("JSON encode failed", "error", err)
	}
}

func (s *WebServer) handleAPIControl(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse command
	cmd := r.FormValue("cmd")

	var err error
	switch cmd {
	case "light_on":
		err = session.Client.MQTT.SetChamberLight(true)
	case "light_off":
		err = session.Client.MQTT.SetChamberLight(false)
	case "speed_silent":
		err = session.Client.MQTT.SetSpeedProfile("1")
	case "speed_std":
		err = session.Client.MQTT.SetSpeedProfile("2")
	case "speed_sport":
		err = session.Client.MQTT.SetSpeedProfile("3")
	case "speed_ludi":
		err = session.Client.MQTT.SetSpeedProfile("4")
	case "pause":
		err = session.Client.MQTT.PausePrint()
	case "resume":
		err = session.Client.MQTT.ResumePrint()
	case "stop":
		err = session.Client.MQTT.StopPrint()
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
	session, ok := s.getSession(r)
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
	session, ok := s.getSession(r)
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
	session, ok := s.getSession(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	// Sanitize path? For now assume trusted user.
	remotePath := filepath.Join(path, header.Filename)

	if err := session.Client.File.Upload(remotePath, file); err != nil {
		slog.Error("Upload failed", "path", remotePath, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleAPIPrint(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	// 1. Upload file
	remotePath := filepath.Join("/", header.Filename) // Root dir
	if err := session.Client.File.Upload(remotePath, file); err != nil {
		slog.Error("Upload failed during print start", "path", remotePath, "error", err)
		http.Error(w, "Upload failed: "+err.Error(), http.StatusInternalServerError)
		return
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
	if err := session.Client.MQTT.StartPrint(remotePath, opts); err != nil {
		slog.Error("Start print failed", "path", remotePath, "error", err)
		http.Error(w, "Start print failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *WebServer) handleCamera(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(r)
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
		// Current implementation supports a single viewer.

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

// Helpers

func (s *WebServer) getSessionID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("bambulan_session")
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

func (s *WebServer) getSession(r *http.Request) (*Session, bool) {
	id, ok := s.getSessionID(r)
	if !ok {
		// No session cookie, check for auth cookie to auto-login
		return s.tryRestoreSession(r)
	}

	s.Mu.RLock()
	session, exists := s.Sessions[id]
	s.Mu.RUnlock()

	if exists {
		return session, true
	}

	// Session ID exists in cookie but not in memory (restart?). Try to restore.
	return s.tryRestoreSession(r)
}

func (s *WebServer) tryRestoreSession(r *http.Request) (*Session, bool) {
	cookie, err := r.Cookie("bambulan_auth")
	if err != nil || cookie.Value == "" {
		return nil, false
	}

	dataBytes, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, false
	}

	var authData map[string]string
	if err := json.Unmarshal(dataBytes, &authData); err != nil {
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
		Status: &bambulan.PrinterStatus{},
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

	// Note: Session restoration currently does not update the session cookie in the response,
	// relying on the client to send the correct session ID or re-authenticate if needed.
	return session, true
}
