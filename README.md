<div style="text-align: center;">
![BambuLAN Logo](assets/bambulan.png)
</div>

# BambuLAN

A Go library for interacting with Bambu Lab 3D printers over the local network (LAN mode).

This library allows you to monitor printer status, control print jobs, view the camera stream, and manage files without relying on the Bambu Lab cloud service.

## Features

- **LAN Control**: Connects directly to the printer's MQTT broker (port 8883).
- **Status Monitoring**: Receive real-time updates on temperatures, fans, print progress, and more.
- **Commands**:
    - Control prints (`print start <file>`, `print pause`, `print resume`, `print stop`).
    - Set print speed profiles (`silent`, `standard`, `sport`, `ludicrous`).
    - Control chamber lights.
    - Send raw G-Code (single line).
    - Dump raw printer info (JSON).
- **Camera Streaming**: Connect to the printer's camera stream (MJPEG over TCP/TLS port 6000).
- **File Management**: List and download files (timelapses, models) via FTPS (port 990).

A CLI built using this library, `bambulan`, also acts as a web server providing a dashboard to monitor and control the printer.

## Installation

```bash
go get github.com/gonzalop/bambulan
```

## Usage

### Connecting and Monitoring

```go
package main

import (
    "fmt"
    "log"
    "github.com/gonzalop/bambulan"
)

func main() {
    // 1. Define configuration
    host := "192.168.1.50"
    accessCode := "12345678" // Found in printer settings
    serial := "01S00A..."

    // 2. Define a callback for status updates
    onUpdate := func(status *bambulan.PrinterStatus) {
        fmt.Printf("Nozzle: %.1f°C | Bed: %.1f°C | Progress: %d%%\n",
            status.NozzleTemp, status.BedTemp, status.McPercent)
    }

    // 3. Initialize and Start Client
    client := bambulan.NewClient(host, accessCode, serial, onUpdate)

    if err := client.Start(); err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }
    defer client.Stop()

    // Keep running...
    select {}
}
```

### Sending Commands

```go
// Turn light on
client.MQTT.SetChamberLight(true)

// set speed to "Sport"
client.MQTT.SetSpeedProfile("3")

// Pause print
client.MQTT.PausePrint()
```

### Camera Capture

```go
// Capture a single JPEG frame
imgData, err := client.Camera.CaptureFrame()
if err != nil {
    log.Println("Error capturing frame:", err)
}
// imgData contains the JPEG bytes
```

### File Management

```go
// List .3mf files
files, err := client.File.GetFiles("/", ".3mf")
if err != nil {
    log.Println("Error listing files:", err)
}

// Download a file
err := client.File.DownloadFile("/timelapse/video.mp4", "./video.mp4")
```

## CLI Tool / Web Interface

The included `cmd/bambulan` builds into a powerful CLI tool named `bambulan`, which also includes a web interface.

![BambuLAN Dashboard](assets/dashboard.png)

See [cmd/bambulan/README.md](cmd/bambulan/README.md) for full usage instructions.

## Acknowledgements

This project builds upon the hard work of the community in reverse-engineering the Bambu Lab network protocol. Special thanks to:

-   [bambu-connect](https://pypi.org/project/bambu-connect/) (Python)
-   [markhaehnel/bambulab](https://github.com/markhaehnel/bambulab) (Rust)

## Disclaimer

This library is not affiliated with or endorsed by Bambu Lab. Use it at your own risk. Protocol details were reverse-engineered from open-source community efforts.
