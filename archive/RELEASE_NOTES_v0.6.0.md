# Release Notes v0.6.0

This release focuses on improving the performance and responsiveness of the web interface through a fully event-driven architecture, while also adding new management capabilities to the CLI and refining the library's API.

## 📡 Event-Driven Architecture
We have completely refactored the internal communication to a multi-subscriber, event-driven pattern.
*   **Multi-Subscriber Support**: The MQTT client now supports multiple concurrent subscribers via `client.Subscribe()`, allowing different parts of the application to receive status updates independently.
*   **API Refinement**: Removed the deprecated `OnUpdate` callback from `NewClient` in favor of the more robust and flexible `Subscribe()` method. This simplifies client initialization and improves resource management.

## ⚡ Web UI Performance
The web dashboard is now faster and more bandwidth-efficient:
*   **Server-Sent Events (SSE)**: Replaced polling with a persistent SSE connection (`/api/events`) for real-time printer status updates.
*   **Delta Updates**: The server now intelligently computes and sends only changed fields (deltas) over SSE, significantly reducing bandwidth usage and browser processing overhead.

## 🛠️ CLI Enhancements
*   **Recursive Downloads**: Added the `download-dir` command to the `file` module. Use `bambulan file download-dir <remote> <local> --recursive` to download entire directories from the printer easily.

## 📖 Documentation
*   **Updated Examples**: All documentation and examples (including `doc.go` and `client.go`) have been updated to reflect the new subscription-based pattern.

## 📦 Dependency Updates
*   Updated `github.com/gonzalop/mq` to `v0.9.5`.
*   Updated `github.com/alecthomas/kong` to `v1.15.0`.
