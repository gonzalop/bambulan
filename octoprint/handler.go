package octoprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/gonzalop/bambulan"
)

// SessionFunc is a callback that the Handler uses to retrieve the active
// bambulan Client and PrinterStatus for an incoming request. The WebServer
// implements this so the handler does not depend on WebServer internals.
type SessionFunc func() (*bambulan.Client, *bambulan.PrinterStatus, bool)

// Handler is an http.Handler that serves the OctoPrint compatibility API.
// Register its routes with http.HandleFunc or a mux — see RegisterRoutes.
type Handler struct {
	session SessionFunc
	apiKey  string
}

// NewHandler creates a Handler.
//
//   - session: called per-request to obtain the active client and status.
//   - apiKey: the expected value of the X-Api-Key header (or "apikey" query param).
func NewHandler(session SessionFunc, apiKey string) *Handler {
	return &Handler{session: session, apiKey: apiKey}
}

// RegisterRoutes wires all OctoPrint routes into mux.
// Callers typically pass http.DefaultServeMux or their own mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	auth := h.authMiddleware

	mux.HandleFunc("/api/version", auth(h.handleVersion))
	mux.HandleFunc("/api/connection", auth(h.handleConnection))
	mux.HandleFunc("/api/printer", auth(h.handlePrinter))
	mux.HandleFunc("/api/printer/command", auth(h.handleCommand))
	mux.HandleFunc("/api/printer/printhead", auth(h.handlePrinthead))
	mux.HandleFunc("/api/printer/bed", auth(h.handleBed))
	mux.HandleFunc("/api/printer/chamber", auth(h.handleChamber))
	mux.HandleFunc("/api/printer/tool", auth(h.handleTool))
	mux.HandleFunc("/api/printerprofiles", auth(h.handlePrinterProfiles))
	mux.HandleFunc("/api/timelapse", auth(h.handleTimelapse))
	mux.HandleFunc("/api/timelapse/download/", auth(h.handleTimelapseDownload))
	mux.HandleFunc("/api/job", auth(h.handleJob))

	// Auth and Apps
	mux.HandleFunc("/api/login", h.handleLogin)
	mux.HandleFunc("/api/apps/auth", h.handleAppsAuth)
	mux.HandleFunc("/api/apps/auth/", h.handleAppsAuth)
	mux.HandleFunc("/plugin/appkeys/request/", h.handleAppsAuth)

	// File management — order matters: the specific path prefix must come first.
	// /api/files/local/<path> handles GET (single entry) and DELETE.
	// /api/files/local handles GET (list) and POST (upload).
	// /api/files handles GET (list all).
	mux.HandleFunc("/api/files/local/", auth(h.handleFileItem)) // trailing slash catches sub-paths
	mux.HandleFunc("/api/files/local", auth(h.handleFiles))
	mux.HandleFunc("/api/files", auth(h.handleFiles))
}

// authMiddleware validates the OctoPrint API key before calling next.
func (h *Handler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("OctoPrint API request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		key := r.Header.Get("X-Api-Key")
		if key == "" {
			key = r.URL.Query().Get("apikey")
		}
		if h.apiKey == "" || key != h.apiKey {
			slog.Warn("OctoPrint API: Invalid or missing API key", "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "Invalid API Key", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// getAdapter returns an Adapter for the current request, or writes an error
// response and returns nil if no active session is available.
func (h *Handler) getAdapter(w http.ResponseWriter) (*Adapter, *bambulan.PrinterStatus, bool) {
	client, status, ok := h.session()
	if !ok {
		http.Error(w, "No active printer session", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	return NewAdapter(client), status, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("OctoPrint: JSON encode failed", "error", err)
	}
}

// handleVersion serves GET /api/version.
func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
	// Version does not need an active session — slicers probe this first.
	adapter := NewAdapter(nil)
	writeJSON(w, http.StatusOK, adapter.Version())
}

// handleConnection serves GET /api/connection.
func (h *Handler) handleConnection(w http.ResponseWriter, r *http.Request) {
	adapter := NewAdapter(nil)
	writeJSON(w, http.StatusOK, adapter.Connection())
}

// handlePrinter serves GET /api/printer.
func (h *Handler) handlePrinter(w http.ResponseWriter, r *http.Request) {
	adapter, status, ok := h.getAdapter(w)
	if !ok {
		return
	}

	resp, err := adapter.PrinterState(r.Context(), status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleJob serves GET and POST /api/job.
func (h *Handler) handleJob(w http.ResponseWriter, r *http.Request) {
	adapter, status, ok := h.getAdapter(w)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err := adapter.JobState(r.Context(), status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, resp)

	case http.MethodPost:
		var cmd JobCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		err := adapter.ExecuteJobCommand(r.Context(), cmd, status)
		if err != nil {
			var conflict *ConflictError
			if errors.As(err, &conflict) {
				http.Error(w, conflict.Message, http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleCommand serves POST /api/printer/command.
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}

	var cmd PrinterCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := adapter.ExecutePrinterCommand(r.Context(), cmd); err != nil {
		slog.Error("OctoPrint: G-code command failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handlePrinthead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cmd PrintheadCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}
	if err := adapter.ExecutePrintheadCommand(r.Context(), cmd); err != nil {
		slog.Error("Printhead command failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleBed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cmd BedCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}
	if err := adapter.ExecuteBedCommand(r.Context(), cmd); err != nil {
		slog.Error("Bed command failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleChamber(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cmd ChamberCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}
	if err := adapter.ExecuteChamberCommand(r.Context(), cmd); err != nil {
		slog.Error("Chamber command failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cmd ToolCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}
	if err := adapter.ExecuteToolCommand(r.Context(), cmd); err != nil {
		slog.Error("Tool command failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handlePrinterProfiles(w http.ResponseWriter, r *http.Request) {
	adapter, status, ok := h.getAdapter(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, adapter.PrinterProfiles(status))
}

func (h *Handler) handleTimelapse(w http.ResponseWriter, r *http.Request) {
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}
	resp, err := adapter.ListTimelapses(r.Context(), serverBaseURL(r))
	if err != nil {
		slog.Error("Timelapse listing failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleTimelapseDownload(w http.ResponseWriter, r *http.Request) {
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}

	// Extract filename from path: /api/timelapse/download/<name>
	name := strings.TrimPrefix(r.URL.Path, "/api/timelapse/download/")
	if name == "" {
		http.Error(w, "Missing filename", http.StatusBadRequest)
		return
	}

	remotePath := path.Join("/timelapse", name)
	reader, err := adapter.client.File.Download(r.Context(), remotePath)
	if err != nil {
		slog.Error("Timelapse download failed", "path", remotePath, "error", err)
		http.Error(w, "Failed to download timelapse", http.StatusNotFound)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", name))

	if _, err := io.Copy(w, reader); err != nil {
		slog.Error("Timelapse stream failed", "error", err)
	}
}

// handleFiles serves GET /api/files, GET /api/files/local (listing)
// and POST /api/files/local (file upload + optional auto-print).
func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleFileListing(w, r, "/")
	case http.MethodPost:
		h.handleFileUpload(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFileItem serves GET and DELETE on /api/files/local/<path>.
func (h *Handler) handleFileItem(w http.ResponseWriter, r *http.Request) {
	// Strip the route prefix to get the relative file path.
	relPath := strings.TrimPrefix(r.URL.Path, "/api/files/local/")
	relPath = path.Clean(relPath)
	absPath := "/" + relPath

	switch r.Method {
	case http.MethodGet:
		h.handleFileListing(w, r, absPath)
	case http.MethodDelete:
		adapter, _, ok := h.getAdapter(w)
		if !ok {
			return
		}
		if err := adapter.DeleteFile(r.Context(), absPath); err != nil {
			slog.Error("OctoPrint: delete failed", "path", absPath, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFileListing lists files in dir and writes a FilesResponse.
func (h *Handler) handleFileListing(w http.ResponseWriter, r *http.Request, dir string) {
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}

	recursive := r.URL.Query().Get("recursive") == "true"
	resp, err := adapter.ListFiles(r.Context(), dir, recursive, serverBaseURL(r))
	if err != nil {
		slog.Error("OctoPrint: file listing failed", "dir", dir, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleFileUpload handles POST /api/files/local (slicer upload + optional print).
func (h *Handler) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	adapter, _, ok := h.getAdapter(w)
	if !ok {
		return
	}

	// OctoPrint clients can send large gcode files.
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

	filename := header.Filename
	remotePath := path.Clean("/" + filename)
	shouldPrint := r.FormValue("print") == "true"

	slog.Info("OctoPrint: upload starting", "filename", filename, "size", header.Size)

	resp, err := adapter.UploadAndPrint(r.Context(), file, remotePath, filename, shouldPrint)
	if err != nil {
		slog.Error("OctoPrint: upload failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// serverBaseURL returns the scheme+host of the incoming request, used to
// construct absolute refs URLs in file listing responses.
func serverBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Respect X-Forwarded-Proto if behind a reverse proxy
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

// handleLogin serves POST /api/login.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	slog.Info("OctoPrint Login request", "method", r.Method, "remote", r.RemoteAddr)
	// Simple mock login: if an API key is provided and valid, return a successful session.
	// This is often probed by HA and slicers to verify connectivity.
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		key = r.URL.Query().Get("apikey")
	}

	active := h.apiKey != "" && key == h.apiKey
	resp := LoginResponse{
		Name:   "admin",
		Active: active,
		User:   true,
		Admin:  true,
	}
	if active {
		resp.Apikey = h.apiKey
		resp.Session = "bambulan-session"
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleAppsAuth handles the OctoPrint "Application Keys" flow used by Home Assistant.
// It automatically approves any request to simplify the cloud-free setup experience.
func (h *Handler) handleAppsAuth(w http.ResponseWriter, r *http.Request) {
	slog.Info("OctoPrint Apps Auth request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
	slog.Debug("OctoPrint: Auth request received", "method", r.Method, "path", r.URL.Path)

	switch r.Method {
	case http.MethodPost:
		var body struct {
			App string `json:"app"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			slog.Warn("OctoPrint: Failed to decode auth request body", "error", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		slog.Info("OctoPrint: Received application auth request (Step 1/2)", "app", body.App)

		// Set Location header for polling
		// We use the current path to ensure consistency regardless of trailing slash
		pollURL := serverBaseURL(r) + r.URL.Path
		if !strings.HasSuffix(pollURL, "/") {
			pollURL += "/"
		}
		pollURL += "bambulan-token"

		w.Header().Set("Location", pollURL)

		resp := struct {
			AppToken string `json:"app_token"`
		}{
			AppToken: "bambulan-token",
		}
		writeJSON(w, http.StatusCreated, resp)

	case http.MethodGet:
		slog.Info("OctoPrint: Received auth polling request (Step 2/2), granting access")
		// When polling, HA expects a 200 OK with the api_key if granted.
		resp := struct {
			APIKey string `json:"api_key"`
		}{
			APIKey: h.apiKey,
		}
		writeJSON(w, http.StatusOK, resp)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
