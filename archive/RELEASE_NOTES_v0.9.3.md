# Release Notes v0.9.3

This release is mostly internal cleanup: MQTT message decoding, Home Assistant discovery, and printer model aliases are simpler now. It also updates core dependencies.

## 🔧 Internals & Refactoring
* **Simpler MQTT Decoding**: Incoming MQTT messages are decoded with one wrapper pass, and the `print` section is merged straight into the printer status. Before, the payload was parsed up to three times. Status updates are only broadcast when a message actually contains a `print` or `info` section.
* **Home Assistant Discovery Factory**: Simplified how entity discovery configs are built in `homeassistant/discovery.go`.
* **Model Aliases**: Removed hard-coded model aliases (`p1s`, `p1p`, `x1`, `x1e`, `a1`, `a1mini`, `x1carbon`). They now come from normalizing each model's display name. `x1c` is still an explicit alias.

## 📦 Dependencies & Toolchain
* **Dependency Updates**:
  * Bumped `github.com/gonzalop/mq` to `v0.10.0`.
  * Bumped `github.com/gonzalop/ftp` to `v1.7.0`.
  * Bumped `github.com/alecthomas/kong` to `v1.16.1`.
