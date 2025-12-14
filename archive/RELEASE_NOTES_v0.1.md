# BambuLAN Initial Release v0.1.0

We are excited to announce the first open-source release of **BambuLAN**, a robust Go library and CLI tool for interacting with Bambu Lab 3D printers over the local network (LAN).

## 🚀 Key Features

### 📦 Go Library (`github.com/gonzalop/bambulan`)
A comprehensive library to build your own tools for Bambu Lab printers.
- **MQTT Client**: Monitor printer status and send commands (printing, fan control, lights, speed).
- **FTPS Client**: List, upload, and download files from the printer's SD card securely.
- **Camera Client**: Stream MJPEG video directly from the printer's camera.
- **Auto-Discovery**: (Upcoming) helpers for connecting via IP and Access Code.

### 🛠️ CLI Tool (`cmd/bambulan`)
A powerful command-line interface for quick interactions.
- **Status Monitoring**: Get real-time printer status (temps, progress, stage).
- **Control**: Pause, resume, stop prints, and control lights/fans.
- **File Management**: List and transfer files.

### 🌐 Web Dashboard
A built-in, modern web interface to monitor and control your printer from any browser.
- **Live Dashboard**: View temperatures, speeds, print progress, and camera feed.
- **Control Panel**: Toggle lights, change speed profiles, and manage print jobs.
- **File Manager**: Browse, upload, and download G-Code/3MF files.
- **Secure**: Features secure session management and XSS protection.

## 🔒 Security
- **Secure Sessions**: Uses cryptographically secure session IDs and hardened cookies (HttpOnly, SameSite).
- **Input Sanitization**: Web interface includes XSS protection for file listings.

## 🛠️ Getting Started

### Installation
```bash
go install github.com/gonzalop/bambulan/cmd/bambulan@latest
```

### Usage
```bash
# Configure via environment variables
export BAMBULAN_HOST=192.168.1.100
export BAMBULAN_CODE=ACCESS_CODE
export BAMBULAN_SERIAL=SERIAL

# Start the web dashboard
bambulan web

# Get status via CLI
bambulan status
```

## 🤝 Contributing
Contributions are welcome! Please verify your code using `golangci-lint` and ensure Godoc comments are strictly formatted.

---
*Developed with ❤️ for the 3D printing community.*
