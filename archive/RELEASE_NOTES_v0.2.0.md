Release Notes v0.2.0

🚀 New Features

 * Custom FTPS Dialer: Implemented a custom dialer for FTPS connections to reliably handle passive mode (PASV) and fix 0.0.0.0 IP issues often seen with Bambu printers. This ensures file uploads and downloads work across different network segments.
 * Enhanced CLI Shortcuts: Added sensible single-letter shortcuts for all CLI options (e.g., -H for host, -c for code, -l for log-level, -a for AMS), improving usability.
 * Web Dashboard Improvements:
     * Added AMS information (humidity, tray details) to the dashboard.
     * Added layer information display.
     * Improved web server security and adhered to better Go conventions.
 * CLI Status & Monitoring:
     * Added a -watch (-w) option to the status command for real-time monitoring.
     * Added dump-info command to output raw printer status as JSON.
     * Displayed fan speeds as percentages in the CLI.
     * Added --show-ams option to explicitly show AMS details (now auto-detected).
 * MQTT Enhancements:
     * MQTT actions now return the sequence ID, allowing for better command tracking.
     * Added explicit KeepAlive and Ping timeouts to improve connection stability.

🛠  Improvements & Refactoring

 * Documentation: Significant improvements to GoDoc comments, clearer usage examples, and internal refactoring of message structs for a cleaner API.

🐛 Bug Fixes

 * Fixed examples in the README and clarified -bed-type options.
 * Various UI fixes for the web interface.
 * General minor fixes and stability improvements.
