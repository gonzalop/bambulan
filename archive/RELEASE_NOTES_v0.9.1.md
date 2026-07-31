# Release Notes v0.9.1

This release improves compatibility with Home Assistant by maintaining dual-key state publishing for existing entities.

## 🏠 Home Assistant Integration
* **Backward-Compatible Entity Payload Keys**: Added dual-key payload publishing (`progress`/`print_progress`, `nozzle_temp`/`nozzle_temperature`, `bed_temp`/`bed_temperature`, `fan_part`/`part_cooling_fan`, `fan_aux`/`aux_fan`, `fan_chamber`/`chamber_fan`, `hms_active`, etc.). This ensures existing Home Assistant entities configured under older discovery schemas continue updating in real-time without getting stuck.
