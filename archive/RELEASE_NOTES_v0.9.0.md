# Release Notes v0.9.0

This release expands BambuLAN's printer capabilities and error code database, adds new print stage descriptions (codes 67–77), introduces file name reporting for Home Assistant, and improves documentation and test coverage.

## 🖨️ Printer Capabilities
* **Bambu Lab A2L (N9) Capabilities**: Added model `N9` (Bambu Lab A2L) capability definitions to `printer_capabilities.json`.

## 📊 Print Stage Expansion (Codes 67–77)
* Added stage descriptions for print stage codes 67 through 77:
  * `67`: Measuring Rotary Attachment
  * `68`: The toolhead moves above the purge chute
  * `69`: Cooling down the nozzle
  * `70`: The toolhead moves to the center of the heatbed
  * `71`: Active Arc Fitting
  * `72`: Hotend Type Detection
  * `73`: Build plate alignment detection
  * `74`: Heatbed surface foreign object detection
  * `75`: Heatbed underside foreign object detection
  * `76`: Pre-extrusion before printing
  * `77`: Preparing AMS

## 🚨 HMS Error Code Database
* **5,223 HMS Error Codes**: Expanded the embedded HMS error code database (`codes_generated.go`) from 519 to **5,223 error codes** to provide comprehensive offline HMS error message lookups.

## 🏠 Home Assistant Integration & Documentation
* **Print File Name Sensor**: Added `subtask_name` sensor to MQTT discovery and telemetry state to expose the active print file name (with `"Idle"` state when no job is active).
* **Dual-Key State Compatibility**: Added dual-key payload publishing (`progress`/`print_progress`, `nozzle_temp`/`nozzle_temperature`, etc.) so that existing entities registered under older discovery schemas continue updating live in real-time without getting stuck.
* **Dashboard Template**: Updated `homeassistant/dashboard.yaml` to include a dedicated File Name tile card and synchronized entity schemas.
* **Bridge Documentation**: Documented standalone CLI (`bambulan ha`) vs. web mode (`bambulan web`) bridge behaviors, MQTT authentication requirements, and dashboard template customization helpers.

## 🧪 Testing & CI
* Added unit test coverage for A2L capability lookups, print stage code resolution, and HMS code lookups.
* Updated GitHub Actions CI test workflow.
