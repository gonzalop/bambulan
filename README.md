![BambuLAN Logo](assets/bambulan.png)

# BambuLAN

**BambuLAN** is a high-performance Go ecosystem for complete, cloud-free management of Bambu Lab 3D printers over the local network (LAN mode).

It provides a robust developer library, a powerful CLI tool, and a modern, real-time web dashboard—allowing you to monitor status, control hardware, view camera streams, and manage files with total privacy and near-zero latency.

## Features

- **Real-time Monitoring**: Receive updates on temperatures, fans, print progress, and detailed printer states.
- **Full Printer Control**:
    - Manage print jobs (`start`, `pause`, `resume`, `stop`, `skip objects`).
    - Set print speed profiles (`silent`, `standard`, `sport`, `ludicrous`).
    - Control temperatures (nozzle/bed) and fan speeds (part/aux/chamber).
    - Toggle hardware options (lights, sound, camera, filament tangle detection).
- **Advanced AMS Support**:
    - Load/Unload filament and control AMS units.
    - Configure filament types, colors, and linear advance (K-values).
    - Monitor AMS humidity and tray status.
- **3MF Project Inspection**: Deep extraction of metadata, plate info, thumbnails, and filament requirements via the `bambu3mf` package.
- **Filament Management**: Resolve filament inheritance, profiles, and compatibility via the `filament` package.
- **Hardware Intelligence**: Automatic model detection (X1, P1, A1 series) to enforce hardware-specific safety limits and capabilities.
- **Camera Streaming**: Access live MJPEG streams and capture static frames.
- **File Management**: Full FTPS support for listing, downloading, uploading, and managing files/directories on the SD card.

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

    // 2. Initialize and Start Client
    client := bambulan.NewClient(host, accessCode, serial)
    if err := client.Start(); err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }
    defer client.Stop()

    // 3. Subscribe to updates
    sub := client.Subscribe()
    defer sub.Cancel()

    go func() {
        for status := range sub.C {
            fmt.Printf("Nozzle: %.1f°C | Bed: %.1f°C | Progress: %d%%\n",
                status.NozzleTemp, status.BedTemp, status.McPercent)
        }
    }()

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

// 1. Upload the file first
err := client.File.UploadFile("./my-model.gcode.3mf", "/my-model.gcode.3mf", nil)
if err != nil {
    log.Fatal(err)
}

// 2. Start the print
opts := bambulan.PrintOptions{
    BedType:     "textured_plate",
    BedLeveling: true,
}
client.MQTT.StartPrint("my-model.gcode.3mf", opts)
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
err := client.File.DownloadFile("/timelapse/video.mp4", "./video.mp4", nil)

// Download a directory recursively
err := client.File.DownloadDirectory("/timelapse", "./backups", true, nil)

// Upload a file with progress
onProgress := func(current, total int64) {
    fmt.Printf("Uploaded %d/%d bytes\n", current, total)
}
err := client.File.UploadFile("./model.gcode.3mf", "/model.gcode.3mf", onProgress)
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
