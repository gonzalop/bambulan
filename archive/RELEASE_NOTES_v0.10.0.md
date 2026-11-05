# Release Notes v0.10.0

This release is mainly about security and reliability. It fixes a web login bypass, an XSS in the filament dialog, and several path traversal and memory exhaustion issues. It also fixes data races across the MQTT client, Home Assistant bridge and web server. Folders can now be deleted from the web UI, and the Bambu Lab H3C is added to the printer capabilities.

## 🔒 Security
* **Login Bypass**: The web server now checks the access code before reusing a pooled printer connection. A live connection with a different code is refused, and a dropped one is replaced only if the new code connects.
* **Filament Dialog XSS**: The current filament name comes from the printer and was inserted as HTML. It is now shown as plain text. A test fails if printer data is written with `innerHTML`.
* **Sessions**: Session IDs are cryptographically random, logout expires the `bambulan_auth` cookie, sessions idle for 24h expire, and at most 256 sessions are kept (the least recently used is evicted). Logins are rate-limited per IP, and the server sets header and idle timeouts.
* **Path Traversal**: Uploaded file names are sanitized, and directory downloads skip entries that would escape the target folder. Names that only start with dots (like `..cache`) are still allowed.
* **3MF Decompression Bombs**: XML and JSON inside 3MF files are read with a size limit.
* **Camera Memory Limit**: The camera stream drops data without a JPEG start marker and gives up on unfinished frames over 2 MB, so a bad or fake host can't exhaust memory.
* **Prototype Pollution**: The dashboard's `mergeDeep` skips `__proto__`, `constructor` and `prototype`.

## 📁 File Management
* **Delete Folders**: The Delete button now removes a folder and everything in it. Before, it always sent the FTP file-delete command, which the printer rejects with error 550 for any folder. Deleting the root folder is refused, and symlinks to folders are never followed. The confirmation dialog says when a whole folder will be deleted.
* **Safe Downloads**: Downloads go to a temporary file that is renamed on success, so a failed or cancelled download no longer truncates an existing local file.
* **Cancel Hang**: Cancelling an FTP transfer no longer blocks for up to 10 seconds.
* **Upload Progress**: The progress total no longer counts bytes before the starting offset of a resumed upload.
* **Remote Paths**: Remote FTP paths always use `/`, including on Windows.

## 🖥️ Web Dashboard
* **Live Updates**: The dashboard switches to polling as soon as the browser closes the event stream, instead of freezing. Slow full-status replies no longer overwrite newer live updates, and switching units re-renders from the current state.
* **External Spool**: Tray 254 (external spool) no longer shows "None loaded".
* **Error Handling**: All control requests handle network errors and expired sessions. Starting a print reports errors, and after an upload the file list always reloads and the file input resets.
* **3MF Filaments**: The filament list now shows the first plate's filaments, matching the plate name and thumbnail shown. Before, it mixed grams from different plates.

## 🏠 Home Assistant
* **Graceful Shutdown**: `bambulan ha` stops the bridge and background sync cleanly on Ctrl+C, and discovery publishing stops as soon as the bridge is cancelled.
* **Chamber Target Temperature**: Published for printers with a chamber heater, even when the target is 0.
* **Less Work**: Removed a duplicate file sync after discovery, and AMS discovery only allocates when there are new entities.

## 📡 MQTT & Camera
* **Data Races**: The printer status is protected by a lock and copied with the new `PrinterStatus.Clone`. Home Assistant bridge state and the MQTT client pointer are now race-free.
* **Broadcast Panic**: An SSE client disconnecting can no longer cause a send on a closed channel. Subscriber channels are buffered.
* **Connection Handlers**: New `SetConnectionHandlers` sets connect and disconnect callbacks safely. `Stop` now also stops auto-reconnect.
* **Chamber Light**: Status updates only after a successful publish, and only the chamber light entry changes (the work light state is kept).
* **Status Updates**: A field with an unexpected type no longer drops the rest of the update.
* **Camera Timeout**: Connecting to the camera times out after 5 seconds, covering TCP and TLS. A host that never answered used to hang the stream and the Home Assistant snapshot loop forever.

## 🖨️ Printer Capabilities
* **Bambu Lab H3C (N10)**: Added capability definitions to `printer_capabilities.json`.
* **Stable Output**: `gen-capabilities` now produces the same output on every run.
* **3D Printers Only**: `gen-capabilities` skips machines that can't 3D print, such as Bambu Lab's unreleased laser-only N8. This matches BambuStudio's public builds.

## 📦 Dependencies & Toolchain
* **Go 1.27.3**: The module now requires Go 1.27.3.
* **Dependency Updates**:
  * Bumped `github.com/gonzalop/mq` to `v0.11.0`.
  * Bumped `github.com/gonzalop/ftp` to `v1.8.0`.

## 🧪 Testing
* Added regression tests for the login check, broadcast panic, session expiry and eviction, login rate limit, path traversal, decompression limits, camera frame extraction and handshake timeout, FTP cancellation, atomic downloads, folder deletion and data races.
