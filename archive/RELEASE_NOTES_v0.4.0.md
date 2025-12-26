# Release Notes v0.4.0

This release focuses on hardening security, improving printer capability detection, and refining the library's API structure.

## 🔒 Security & Web Enhancements
*   **HTTPS Support**: The web server now supports running over HTTPS for secure local access.
*   **Cookie Hardening**: Enhanced cookie security configurations to protect session data.

## ⚙️ Capabilities System
We've introduced a robust **Capabilities System** to dynamically adapt to different printer models (X1C, P1P, A1, etc.).
*   **Feature Detection**: The library can now load and check specific printer capabilities (e.g., supported resolutions, fans, speeds) from a `printer_capabilities.json` definition.
*   **UI Adaptation**: The Web Dashboard now hides or disables controls that are not supported by the connected printer model, ensuring a cleaner experience.

## 📹 Camera
*   **RTSP URL Helper**: Added `GetRTSPURL` helper method to easily retrieve the RTSP stream URL for integration with external video players.

## 🛠️ CLI & Library Improvements
*   **Status Output**: Improved filtering and identification in the `bambulan status` command for clearer output.
*   **API Refactoring**: Moved capabilities logic to the library root for easier access by consumers.
*   **Documentation**: Significant updates to library API documentation for better developer experience.

## 📦 Dependency Updates
*   Updated `github.com/gonzalop/ftp` to `v1.2.3` for improved FTP stability.
