# Release Notes v0.7.0

This major release introduces full Slicer Integration via an OctoPrint compatibility layer, robust network cancellation support through Go contexts, and significant refinements to the Web UI and hardware capability engine.

## 🚀 OctoPrint Compatibility (Slicer Integration)
BambuLAN now emulates an OctoPrint server, allowing you to connect your printer directly to OrcaSlicer, PrusaSlicer, and other OctoPrint-compatible tools without relying on the cloud.
*   **One-Click Print**: Upload and start print jobs directly from your slicer.
*   **Remote File Management**: Browse, download, and delete files on the printer's SD card from within your slicer's UI.
*   **Manual Control**: Full support for jogging axes, homing, and setting temperatures (Bed, Nozzle, Chamber) via the slicer's control widgets.
*   **Multi-Extruder Ready**: Added support for multi-nozzle reporting in the OctoPrint API, paving the way for dual-extruder models.
*   **Secure Auth**: Integrated API Key authentication (via `--api-key` or `BAMBULAN_API_KEY`).
*   **Printer Profiles**: Automatic build volume detection based on the printer model (e.g., X1C vs A1 mini).

## 📡 Real-Time Monitoring (SSE)
*   **Server-Sent Events**: The dashboard now utilizes high-performance SSE (`/api/events`) for near-zero latency updates, replacing legacy polling.
*   **Intelligent Deltas**: To minimize bandwidth, the server now only transmits changed fields (deltas) to the client, ensuring the UI remains fluid even on constrained networks.

## 🛡️ Context-Aware Architecture
The core library has been refactored to use `context.Context` for all network-heavy operations.
*   **Robust Cancellation**: In-flight FTP transfers and MQTT commands now respect request cancellation. Aborting an HTTP request now immediately unblocks the underlying network I/O.
*   **Self-Healing Connections**: Implemented a forced-reset pattern for FTP operations that ensures clean recovery and reconnection if a transfer is interrupted.

## 🎨 Web UI Refinements
*   **Dark Mode**: Added a native Dark Mode theme that respects system preferences or can be toggled manually.
*   **Connection Indicator**: A new real-time status badge in the dashboard monitors the health of the connection between the dashboard and the BambuLAN server.

## 🔧 Hardware & Stability
*   **Expanded Printer Support**: Added detailed capability profiles for the full Bambu Lab lineup, including the **A1**, **A1 mini**, **P1S**, and the industrial **X1E**.
*   **Multi-Toolhead Support**: Added foundational support for dual-extruder printer models (e.g., X2D, H2C) within the internal status and API layers.
*   **Chamber Heating**: Added support for printers equipped with active chamber heaters (e.g., X1E).
*   **Env Var Support**: Expanded environment variable support for all global flags, making containerized deployments much simpler.

## 📦 Dependency Updates
*   Updated `github.com/gonzalop/ftp` to include context-aware Quit() logic.
*   Updated `github.com/gonzalop/mq` for better message synchronization.
