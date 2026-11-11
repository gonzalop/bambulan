package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gonzalop/bambulan"
	"github.com/gonzalop/bambulan/homeassistant"
	"github.com/gonzalop/bambulan/internal/bambu3mf"
)

//go:embed templates/*
var templateFS embed.FS

// WebCmd represents the CLI command for starting the web interface.
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

// Session holds the state for an active user session, including the printer client and cached metadata.
type Session struct {
	client                  *bambulan.Client
	status                  *bambulan.PrinterStatus
	csrfToken               string
	uploadStatus            UploadStatus
	metadataDownloadStatus  MetadataDownloadStatus
	currentMetadata         *bambu3mf.Metadata
	currentMetadataFilename string // Track which file the metadata belongs to
	metadataCache           map[string]*bambu3mf.Metadata
	thumbnailCache          map[string][]byte
	failedFetches           map[string]int  // Track retry counts
	fetching                map[string]bool // Track active fetches to prevent thundering herd // key: filename/plate_id
	previousGcodeState      string          // Track previous state to detect transitions
	mu                      sync.RWMutex

	lastSeen atomic.Int64 // Unix seconds of the last request or live update
}

func (s *Session) touch() { s.lastSeen.Store(time.Now().Unix()) }

func (s *Session) deviceModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.DeviceModel
}

func (s *Session) trackUpload(filename string) (progress func(current, total int64), done func()) {
	s.mu.Lock()
	s.uploadStatus = UploadStatus{
		Uploading: true,
		Filename:  filename,
	}
	s.mu.Unlock()

	progress = func(current, total int64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.uploadStatus.Current = current
		s.uploadStatus.Total = total
		if total > 0 {
			s.uploadStatus.Percent = float64(current) / float64(total) * 100
		}
	}
	done = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.uploadStatus = UploadStatus{}
	}
	return progress, done
}

func (s *Session) tryStartFetch(filename string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fetching[filename] || s.failedFetches[filename] >= 3 {
		return false
	}
	s.fetching[filename] = true
	return true
}

func (s *Session) finishFetch(filename string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fetching, filename)
	s.metadataDownloadStatus = MetadataDownloadStatus{}
}

func (s *Session) recordFetchFailure(filename string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedFetches[filename]++
	return s.failedFetches[filename]
}

func (s *Session) setMetadataDownloadProgress(current, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadataDownloadStatus.Current = current
	if total > 0 {
		s.metadataDownloadStatus.Total = total
		s.metadataDownloadStatus.Percent = float64(current) / float64(total) * 100
	}
}

const (
	sessionIdleTimeout = 24 * time.Hour
	maxSessions        = 256
	loginRateWindow    = time.Minute
	loginRateMax       = 10 // login attempts per client IP per window
)

// UploadStatus tracks the progress of a file upload to the printer.
type UploadStatus struct {
	Uploading bool    `json:"uploading"`
	Filename  string  `json:"filename"`
	Current   int64   `json:"current"`
	Total     int64   `json:"total"`
	Percent   float64 `json:"percent"`
}

// MetadataDownloadStatus tracks the progress of downloading 3MF metadata from the printer.
type MetadataDownloadStatus struct {
	Downloading bool    `json:"downloading"`
	Filename    string  `json:"filename"`
	Current     int64   `json:"current"`
	Total       int64   `json:"total"`
	Percent     float64 `json:"percent"`
}

// WebServer manages the HTTP server, user sessions, and active printer connections.
type WebServer struct {
	bindAddr string
	sessions map[string]*Session
	mu       sync.RWMutex
	// Connection pooling
	activeClients map[string]*bambulan.Client
	activeBridges map[string]context.CancelFunc
	clientsMu     sync.Mutex
	key           []byte
	useTLS        bool
	certFile      string
	keyFile       string
	maxFileSize   int64

	// HA MQTT Bridge
	mqttBroker   string
	mqttUser     string
	mqttPassword string
	mqttPrefix   string

	loginMu       sync.Mutex
	loginAttempts map[string]*loginWindow
}

type loginWindow struct {
	start time.Time
	count int
}

// NewWebServer initializes a new WebServer instance with default maps.
func NewWebServer() *WebServer {
	return &WebServer{
		sessions:      make(map[string]*Session),
		activeClients: make(map[string]*bambulan.Client),
		activeBridges: make(map[string]context.CancelFunc),
		loginAttempts: make(map[string]*loginWindow),
		maxFileSize:   bambu3mf.DefaultMaxFileSize,
	}
}

// Run executes the web command, configuring and starting the web server.
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
		fmt.Printf("No secret provided. Generated session secret: %s\n", secret)
		fmt.Println("To restore sessions after a restart, use this secret:")
		fmt.Printf("\tbambulan web --secret \"%s\"\n\n", secret)
	}

	// Derive 32-byte key from secret using SHA-256
	keyHash := sha256.Sum256([]byte(secret))
	key := keyHash[:]

	s := NewWebServer()
	s.bindAddr = c.Bind
	s.key = key
	s.useTLS = c.CertFile != "" && c.KeyFile != ""
	s.certFile = c.CertFile
	s.keyFile = c.KeyFile
	s.maxFileSize = int64(c.MaxFileSize)
	s.mqttBroker = c.MQTTBroker
	s.mqttUser = c.MQTTUser
	s.mqttPassword = c.MQTTPassword
	s.mqttPrefix = c.MQTTPrefix

	return s.Start()
}

// Start registers HTTP handlers and starts listening on the configured address.
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
		Addr:              s.bindAddr,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if s.useTLS {
		slog.Info("Starting web server with TLS", "addr", s.bindAddr, "cert", s.certFile)
		return srv.ListenAndServeTLS(s.certFile, s.keyFile)
	}

	slog.Debug("Starting web server", "addr", s.bindAddr)
	return srv.ListenAndServe()
}

// getClient retrieves an existing client or creates a new one if needed.
// It handles IP changes for the same serial and ensures thread safety.
func (s *WebServer) getClient(host, code, serial string) (*bambulan.Client, error) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	key := serial
	client, exists := s.activeClients[key]

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
		s.activeClients[key] = client
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
		s.activeClients[key] = client
		s.startBridgeLocked(key, client)
	}

	return client, nil
}

var errAccessCodeMismatch = errors.New("access code does not match")

// isConnected reports whether c has a live MQTT session; swapped in tests.
var isConnected = func(c *bambulan.Client) bool { return c.MQTT.IsConnected() }

// startBridgeLocked starts the Home Assistant MQTT bridge for a client if configured.
// s.clientsMu must be held.
func (s *WebServer) startBridgeLocked(key string, client *bambulan.Client) {
	if s.mqttBroker == "" {
		return
	}
	serial := client.MQTT.Serial
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	s.activeBridges[key] = bridgeCancel
	go func() {
		slog.Info("Starting Home Assistant bridge for newly connected printer", "serial", serial, "broker", s.mqttBroker)
		bridge, err := homeassistant.NewBridge(client, s.mqttBroker, s.mqttUser, s.mqttPassword, s.mqttPrefix)
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

// stopClientLocked stops the Home Assistant bridge (if any) and printer client for key.
// s.clientsMu must be held.
func (s *WebServer) stopClientLocked(key string) {
	if cancelBridge, ok := s.activeBridges[key]; ok {
		cancelBridge()
		delete(s.activeBridges, key)
	}
	if client, ok := s.activeClients[key]; ok {
		client.Stop()
		delete(s.activeClients, key)
	}
}
