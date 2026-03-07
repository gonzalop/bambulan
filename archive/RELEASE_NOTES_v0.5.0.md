# Release Notes v0.5.0

This major release introduces deep integration with 3MF files, a redesigned Web Dashboard with rich metadata, and a more robust MQTT foundation.

## 📦 3MF Metadata & Library
We've introduced a new dedicated parsing library, `bambu3mf`, which allows for deep inspection of Bambu Lab project files.
*   **Plate & Filament Info**: Extract detailed information about print plates, including names, thumbnails, and filament requirements.
*   **Thumbnail Support**: The library now handles both small and large plate thumbnails embedded in 3MF files.
*   **Standard Metadata**: Added support for standard 3MF metadata fields.

## 🖥️ Web Dashboard Enhancements
The Web UI has received a significant overhaul to provide a more "alive" and informative experience:
*   **Live Print Metadata**: The dashboard now automatically fetches and displays metadata for the currently active print job.
*   **Visual Previews**: High-quality plate thumbnails are now shown directly in the status card.
*   **Filament Requirements**: View the filaments required for the current print, synchronized with the 3MF project data.
*   **Click-to-Screenshot**: A new button on the camera feed allows you to capture and save the current frame instantly.
*   **Improved Connection Management**: Features a new loading screen and robust error handling during the connection phase.
*   **Session Persistence**: Improved session restoration allows you to pick up where you left off after a browser restart.
*   **UX Refinements**: 
    *   Better layout with the Print Info card positioned for better visibility.
    *   Dynamic adaptation of controls based on printer model capabilities.
    *   Real-time progress bars for both file uploads and metadata synchronization.

## ⚙️ MQTT & Core Library
*   **New MQTT Client**: Migrated to `github.com/gonzalop/mq` for better reconnection logic, cleaner API, and overall improved stability.
*   **Expanded Status Model**: The `PrinterStatus` model now includes dozens of new fields for more granular monitoring.
*   **Detailed AMS Reporting**: Added support for raw humidity data (`humidity_raw`) and expanded tray information.
*   **Comprehensive Stage Descriptions**: Updated the stage mapping to support over 60 different printer states (from nozzle cleaning to BirdsEye calibration).
*   **Automatic Model Detection**: Improved logic for identifying printer models and enforcing hardware-specific limits (e.g., bed temperature).

## 🔒 Security
*   **CSRF Protection**: All state-changing API calls now require a valid CSRF token.
*   **Cookie Hardening**: Further refined session cookie configurations for better security.
*   **XSS Prevention**: Enhanced sanitization for file listings and metadata display.

## 🛠️ Improvements & Bug Fixes
*   **FTP Optimization**: Improved the internal FTP client for faster and more reliable file transfers.
*   **3MF Path Handling**: Fixed issues with `_` prefix filenames often used by the printer for metadata.
*   **API Consistency**: Refactored internal models to reduce cyclomatic complexity and improve maintainability.
*   **Bed Temperature Logic**: Fixed a bug where max bed temperature limits were not correctly applied for certain models.

## 📦 Dependency Updates
*   Updated `github.com/gonzalop/mq` to `v0.9.4`.
*   Updated `github.com/gonzalop/ftp` to `v1.5.0`.
*   Updated `github.com/alecthomas/kong` to `v1.14.0`.
