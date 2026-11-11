package main

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/internal/bambu3mf"
)

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
	session.mu.RLock()
	defer session.mu.RUnlock()
	data := struct {
		Status       *bambulan.PrinterStatus
		Host         string
		CSRFToken    string
		Capabilities bambulan.PrinterCapability
	}{
		Status:       session.status,
		Host:         session.client.MQTT.Hostname,
		CSRFToken:    session.csrfToken,
		Capabilities: bambulan.CapabilitiesForModel(session.status.DeviceModel),
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

	session.mu.RLock()
	defer session.mu.RUnlock()

	data := struct {
		CSRFToken string
	}{
		CSRFToken: session.csrfToken,
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

var (
	activeStates   = map[string]bool{"RUNNING": true, "PAUSE": true, "PREPARE": true}
	terminalStates = map[string]bool{"FINISH": true, "FAILED": true, "IDLE": true}
)

func (s *Session) buildStatusPayload() (map[string]any, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Detect state transitions and clear metadata when print finishes/stops
	currentState := s.status.GcodeState
	previousState := s.previousGcodeState

	if activeStates[previousState] && terminalStates[currentState] {
		slog.Debug("Print state transition detected, clearing metadata", "from", previousState, "to", currentState)
		if s.currentMetadataFilename != "" {
			delete(s.metadataCache, s.currentMetadataFilename)
			slog.Debug("Cleared metadata cache entry", "file", s.currentMetadataFilename)
		}
		s.currentMetadata = nil
		s.currentMetadataFilename = ""
	}

	s.previousGcodeState = currentState

	activeFile := s.status.SubtaskName
	if activeFile == "" {
		activeFile = s.status.GcodeFile
	}
	if len(activeFile) > 0 && activeFile[0] == '/' {
		activeFile = activeFile[1:]
	}

	var fetchFile string

	if activeFile != "" && activeFile != "???" {
		isActive := activeStates[s.status.GcodeState]

		if isActive || s.currentMetadata == nil {
			var foundMD *bambu3mf.Metadata
			var foundFilename string

			// 1. Try sanitized filename first (if starts with _)
			if after, ok := strings.CutPrefix(activeFile, "_"); ok {
				sanitized := after
				if md, ok := s.metadataCache[sanitized]; ok {
					foundMD = md
					foundFilename = sanitized
				}
			}

			// 2. Try exact filename
			if foundMD == nil {
				if md, ok := s.metadataCache[activeFile]; ok {
					foundMD = md
					foundFilename = activeFile
				}
			}

			if foundMD != nil {
				s.currentMetadata = foundMD
				s.currentMetadataFilename = foundFilename
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
		"print":                    s.status,
		"stage_message":            s.status.PrintStageName(),
		"upload_status":            s.uploadStatus,
		"metadata_download_status": s.metadataDownloadStatus,
		"capabilities":             bambulan.CapabilitiesForModel(s.status.DeviceModel),
		"metadata":                 nil,
		"hms": map[string]any{
			"message":     s.status.HMSMessage(),
			"description": s.status.HMSDescription(),
			"urls":        s.status.WikiURLs(),
			"active":      len(s.status.Hms) > 0,
		},
	}

	if s.currentMetadata != nil {
		mdMap := map[string]any{
			"plates":    s.currentMetadata.Plates,
			"filaments": s.currentMetadata.Filaments,
			// Add unified filename field for thumbnails
			"filename": s.currentMetadataFilename,
		}
		resp["metadata"] = mdMap
	}

	return resp, fetchFile
}

// buildStatusPayload constructs the JSON response map for the status API and SSE.
func (s *WebServer) buildStatusPayload(session *Session) map[string]any {
	resp, fetchFile := session.buildStatusPayload()

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

// computeDelta calculates the difference between oldMap and newMap.
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

	sub := session.client.Subscribe()
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
				session.mu.Lock()
				session.status = update
				session.mu.Unlock()
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
	if err := session.client.File.RemoveAll(r.Context(), cleanPath); err != nil {
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

	if err := session.client.File.Rename(r.Context(), cleanOld, cleanNew); err != nil {
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

	if err := session.client.File.MakeDirectory(r.Context(), cleanPath); err != nil {
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
		_, err = session.client.MQTT.SetChamberLight(r.Context(), true)
	case "light_off":
		_, err = session.client.MQTT.SetChamberLight(r.Context(), false)
	case "speed_silent":
		_, err = session.client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedSilent)
	case "speed_std":
		_, err = session.client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedStandard)
	case "speed_sport":
		_, err = session.client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedSport)
	case "speed_ludi":
		_, err = session.client.MQTT.SetSpeedProfile(r.Context(), bambulan.SpeedLudicrous)
	case "pause":
		_, err = session.client.MQTT.PausePrint(r.Context())
	case "resume":
		_, err = session.client.MQTT.ResumePrint(r.Context())
	case "stop":
		_, err = session.client.MQTT.StopPrint(r.Context())
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

	files, err := session.client.File.ListFiles(r.Context(), cleanDir)
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

	reader, err := session.client.File.Download(r.Context(), cleanPath)
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

func sanitizeUploadFilename(name string) (string, bool) {
	clean := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == "/" || clean == ".." || clean == "" {
		return "", false
	}
	return clean, true
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
	cleanFilename, ok := sanitizeUploadFilename(header.Filename)
	if !ok {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	remotePath := path.Join(cleanDir, cleanFilename)

	progressCb, finishUpload := session.trackUpload(cleanFilename)
	defer finishUpload()

	if err := session.client.File.Upload(r.Context(), remotePath, file, progressCb); err != nil {
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

		cleanFilename, ok := sanitizeUploadFilename(header.Filename)
		if !ok {
			http.Error(w, "Invalid filename", http.StatusBadRequest)
			return
		}
		header.Filename = cleanFilename

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
		remotePath = path.Join("/", cleanFilename) // Root dir

		progressCb, finishUpload := session.trackUpload(cleanFilename)
		defer finishUpload()

		if err := session.client.File.Upload(r.Context(), remotePath, file, progressCb); err != nil {
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
	if _, err := session.client.MQTT.StartPrint(r.Context(), remotePath, opts); err != nil {
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

		if _, err := session.client.MQTT.LoadFilament(r.Context(), target); err != nil {
			slog.Error("Load filament failed", "target", target, "error", err)
			http.Error(w, "Load filament failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

	case "unload":
		if _, err := session.client.MQTT.UnloadFilament(r.Context()); err != nil {
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

	if err := session.client.Camera.StartStream(r.Context(), onImage); err != nil {
		slog.Error("Failed to start camera stream", "error", err)
		http.Error(w, "Camera busy", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		writeMu.Lock()
		stopped = true
		writeMu.Unlock()
		session.client.Camera.StopStream()
	}()

	// Wait until client disconnects
	<-r.Context().Done()
}

func (s *WebServer) handleFanControl(w http.ResponseWriter, r *http.Request, session *Session) {
	fan := r.FormValue("fan")
	speedStr := r.FormValue("speed")
	speed, err := strconv.Atoi(speedStr)
	if err != nil {
		http.Error(w, "Invalid speed", http.StatusBadRequest)
		return
	}

	caps := bambulan.CapabilitiesForModel(session.deviceModel())
	if fan == "chamber" && !caps.HasChamberFan {
		http.Error(w, "Chamber fan not supported", http.StatusBadRequest)
		return
	}
	if fan == "aux" && !caps.HasAuxFan {
		http.Error(w, "Aux fan not supported", http.StatusBadRequest)
		return
	}

	if _, err := session.client.MQTT.SetFanSpeed(r.Context(), fan, speed); err != nil {
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

	caps := bambulan.CapabilitiesForModel(session.deviceModel())

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
		_, err = session.client.MQTT.SetNozzleTemperature(r.Context(), temp, index)
	case "bed":
		if temp > caps.MaxBedTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxBedTemp), http.StatusBadRequest)
			return
		}
		_, err = session.client.MQTT.SetBedTemperature(r.Context(), temp)
	case "chamber":
		if !caps.HasChamberHeater {
			http.Error(w, "Chamber heater not supported", http.StatusBadRequest)
			return
		}
		if temp > caps.MaxChamberTemp {
			http.Error(w, fmt.Sprintf("Temperature exceeds limit of %d", caps.MaxChamberTemp), http.StatusBadRequest)
			return
		}
		_, err = session.client.MQTT.SetChamberTemperature(r.Context(), temp)
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
