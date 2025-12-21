This release is a major functional upgrade of the library, introducing full printer control capabilities,  and a robust new FTP client. The CLI and Web UI added support for the new functionality.

## ⚙️  New Printer Control APIs & CLI
We've exposed a comprehensive set of MQTT-based controls for interacting with the printer:
*   **Temperature Control**:
    *   Set target temperatures for both **Nozzle** and **Bed**.
    *   Smart limits based on printer model detection (e.g., A1 mini vs X1C).
    *   CLI: `bambulan temp head <temp>` / `bambulan temp bed <temp>`
*   **Fan Control**:
    *   Independent control for **Part**, **Aux**, and **Chamber** fans.
    *   CLI: `bambulan fan <name> <speed>` (e.g., `bambulan fan aux 80`)
*   **Movement & Speed**:
    *   Change print speed on the fly: **Silent**, **Standard**, **Sport**, **Ludicrous**.
    *   CLI: `bambulan speed <mode>`
*   **Hardware Control**:
    *   Configure **Nozzle Details** (diameter/type) and **Marker Detector** (ArUco).
*   **Advanced Print Management**:
    *   **Skip Objects**: Exclude failing objects from an active print.
    *   **AMS Control**: Load/Unload filament, configure filaments, and update AMS user settings.
    *   **Print Experience**: Toggle options like *Auto Recovery*, *Filament Tangle Detect*, and *Sound*.
*   **FTP Operation**:
    * **More Commands Supported**: Rename, delete, create folder, recursive delete...
## 🖥️ Web Dashboard Overhaul
The dashboard has some updates too:
*   **Interactive Controls**:
    *   **Temperature**: Quick presets (PLA, PETG, ABS) and slide controls for Nozzle/Bed.
    *   **Fans**: Direct percentage control for all system fans.
    *   **Speed & Light**: One-click toggles for speed profiles and chamber lighting.
    *   **Filament Ops**: dedicated modals for loading/unloading filament from specific slots.
*   **File Management**:
    *   **Upload Progress**: Real-time visual progress bar for uploading files to the printer.
    *   **Safety**: "Are you sure?" confirmation modals for destructive actions.
## 🛠️ Internal Improvements
*   **Dedicated FTP Library**: Migrated to `github.com/gonzalop/ftp v1.0.0` for reliable, standard-compliant FTP operations.
*   **Robust Discovery**: Enhanced printer discovery logic to correctly identify IP, Model, and Serial across network interfaces.
## 📦 Dependency Updates
*   Removed `github.com/jlaffaye/ftp` in favor of `github.com/gonzalop/ftp`.
