# BambuLAN CLI / Web Interface

The `bambulan` CLI tool allows you to control and monitor your Bambu Lab printer from the command line.

## Installation

```bash
go install github.com/gonzalop/bambu/cmd/bambulan
```

Or build manually:

```bash
make
```

## Usage

Global flags are required for all commands unless environment variables are set.

**Global Flags:**
- `--host` (`-H`): Printer IP or hostname (Env: `BAMBULAN_HOST`)
- `--code` (`-c`): Access code (Env: `BAMBULAN_CODE`)
- `--serial` (`-s`): Printer serial number (Env: `BAMBULAN_SERIAL`)
- `--log-level` (`-l`): Log level (debug, info, warn, error) (default: "info")

```bash
# Using flags (mixed long/short example)
./bambulan -H <IP> -c <CODE> -s <SERIAL> --log-level debug status

# Using environment variables
export BAMBULAN_HOST="192.168.1.50"
export BAMBULAN_CODE="12345678"
export BAMBULAN_SERIAL="01S00A..."
./bambulan status
```

### Commands

#### Status
Monitor printer status in real-time.
```bash
./bambulan status
# Watch mode:
./bambulan status --watch (-w)
```

#### Dump Info
Dump the full printer status as a JSON object. Useful for debugging or inspecting raw values.
```bash
./bambulan dump-info
```

#### Web Interface
Start the web dashboard (default port 8080).
```bash
./bambulan web
# Access at http://localhost:8080
```

**Features:**
- **Dashboard**: Real-time status monitoring.
- **Login**: Secure access with printer credentials.
- **File Manager**: Browse files, download, and print directly.
- **Print Start**: Upload and start prints with options.

**Screenshots:**

### Dashboard
![Dashboard](../../assets/dashboard.png)

### Login Screen
![Login](../../assets/login-screen.png)

### File Manager
![File Manager](../../assets/file-manager.png)

### Start Print Modal
![Start Print](../../assets/start-print.png)


#### Controls
```bash
# Turn chamber light on/off
./bambulan chamber-light on

# Set print speed (silent, standard, sport, ludicrous)
./bambulan speed sport

# Pause/Resume/Stop print
./bambulan print pause
./bambulan print resume
./bambulan print stop
```

#### Start Print
Uploads a file and starts printing.

```bash
./bambulan print start [options] <filename.gcode|.3mf>
```

**Options:**
- `--bed-type` (`-b`) <string>: Bed type (auto, textured_plate, cool_plate, engineering_plate, high_temp_plate) (default: "auto")
- `--timelapse` (`-t`): Enable timelapse (default: false)
- `--bed-leveling` (`-e`): Enable bed leveling (default: true)
- `--flow-calibration` (`-f`): Enable flow calibration (default: false)
- `--vibration-calibration` (`-v`): Enable vibration calibration (default: true)
- `--layer-inspection` (`-i`): Enable layer inspection (default: false)
- `--use-ams` (`-a`): Use AMS (default: false)
- `--skip-upload`: Skip upload and use the provided path as an existing file on the printer.

#### Camera
Capture a single frame from the camera.
```bash
./bambulan capture [output.jpg]
```

#### File Management
```bash
# List files
./bambulan ls /

# Download file
./bambulan download /timelapse/video.mp4 [./local_video.mp4]
```
#### AMS Filament
Update AMS filament properties (type, color, etc).

**Basic (Manual IDs):**
```bash
./bambulan ams filament \
  --unit 0 --slot 0 \
  --color FFFFFFFF --type "Bambu PLA Basic" \
  --filament-id "GFA00" --setting-id "GFA00_1.75_PLA_..." \
  --min-temp 190 --max-temp 220
```

**Automated Lookup (Recommended):**
Point to your Bambu Studio resources directory (containing JSON definitions) to automatically resolve IDs from a friendly name.

```bash
# Using local resource directory
./bambulan ams filament -u 0 -S 0 -C FFFFFFFF \
  --type "Bambu PLA Wood" \
  --resources ./resources/filament
```

**Options:**
- `--unit` (`-u`): AMS Unit ID (0-3) (default: 0)
- `--slot` (`-S`): Slot ID (0-3) (required)
- `--color` (`-C`): Color in HEX (RRGGBBAA) (required)
- `--type` (`-t`): Filament Type (e.g. 'PLA Basic') OR search term for lookup (required)
- `--filament-id` (`-f`): Filament ID (e.g. 'GFA00'). Optional if lookup finds a match.
- `--setting-id` (`-i`): Setting ID (e.g. 'GFSA16_00'). Optional if lookup finds a match.
- `--resources` (`-R`): Path to filament JSON resources (Env: `BAMBULAN_RESOURCES`)
