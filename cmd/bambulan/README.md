# BambuLAN CLI / Web Interface

The `bambulan` CLI tool allows you to control and monitor your Bambu Lab printer from the command line.

## Installation

```bash
go install github.com/gonzalop/bambu/cmd/bambulan
```

Or build manually:

```bash
go build -o bambulan ./cmd/bambulan
```

## Usage

Global flags are required for all commands unless environment variables are set.

```bash
# Using flags
./bambulan --host <IP> --code <CODE> --serial <SERIAL> status

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
# or with AMS details:
./bambulan status --show-ams

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
- **File Manager**: Browse and download files.
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
- `-bed-type <string>`: Bed type (auto, textured_plate, cool_plate, engineering_plate, high_temp_plate) (default: "auto")
- `-timelapse`: Enable timelapse (default: false)
- `-bed-leveling`: Enable bed leveling (default: true)
- `-flow-calibration`: Enable flow calibration (default: false)
- `-vibration-calibration`: Enable vibration calibration (default: true)
- `-layer-inspection`: Enable layer inspection (default: false)
- `-use-ams`: Use AMS (default: false)

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

