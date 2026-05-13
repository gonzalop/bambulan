# Release Notes v0.8.0

This release focuses on a complete, production-ready Home Assistant integration via MQTT Discovery, significant CLI improvements, and a strategic refactoring of the internal service architecture.

## 🏠 Advanced Home Assistant Integration
BambuLAN now provides a high-efficiency bridge to Home Assistant, allowing for deep integration of your printer into your home automation ecosystem.

*   **Zero-Config Discovery**: Fully supports MQTT Discovery with abbreviated keys and base topic (`~`) optimization for near-instant setup.
*   **Stable & Clean Naming**: Implemented strict `object_id` overrides to ensure consistent entity IDs (`bambu_lab_<model>_<serial>_...`) across all your devices.
*   **Modern Dashboard Template**: A pre-configured [Dashboard Template](homeassistant/dashboard.yaml) is now included, featuring gauges, live camera previews, and categorized controls.
*   **Capability-Aware**: Discovery is now intelligent—sensors and controls for chamber heaters, auxiliary fans, or AMS slots only appear if your specific printer model supports them.
*   **Full Hardware Control**:
    *   **Fans**: Direct control of Part, Aux, and Chamber fans with automatic 0-15 step scaling.
    *   **Safety Limits**: All temperature controls (Nozzle, Bed, Chamber) now enforce hardware-specific safety limits defined in the capabilities database.
    *   **Action Buttons**: Trigger SD card file refreshes, pauses, resumes, and stops directly from the HA UI.
*   **MQTT 5.0 Optimized**: Support for **Topic Aliases** significantly reduces bandwidth overhead on compatible brokers.

## 🛠️ CLI Enhancements
*   **System Diagnostics**: Added the `sys-info` command to quickly inspect hardware versions, firmware, network health, and printer capabilities from the terminal.
*   **Reliable Printing**: The `print` command now correctly waits for AMS status synchronization before verifying filament presence, preventing "missing filament" false positives on startup.

## ⚠️ Breaking Changes
*   **OctoPrint Emulation Removed**: The OctoPrint compatibility layer (Slicer Integration) has been removed to simplify the codebase and focus on the native Home Assistant and Web Dashboard experiences.

## 📦 Technical & Performance
*   **Abbreviated MQTT Payloads**: Reduced discovery message size by ~60% using standard Home Assistant MQTT abbreviations.
*   **Library Updates**: Updated `github.com/gonzalop/mq` to the latest version to support MQTT 5.0 features.
*   **Enhanced State Mapping**: State updates are now published as a single consolidated JSON payload, reducing the number of MQTT messages and improving overall bridge responsiveness.
