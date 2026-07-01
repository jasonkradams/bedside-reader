# Bedside Audiobook Appliance

A custom-built bedside clock and audiobook player designed for distraction-free listening. Built around a Raspberry Pi Zero, an audio amplifier, and a custom 3D-printed enclosure generated dynamically using Fusion 360's Python API.

## Features

- **Distraction-Free Audio:** Plays audiobooks and media reliably using a custom Go daemon.
- **Hardware Integration:** Supports I2C display HATs, a rotary encoder for volume and track control, and physical plunge buttons.
- **Parametric 3D Enclosure:** Includes automated Fusion 360 Python scripts (`hardware/fusion360`) to procedurally generate and export the exact 3D models required for the case, rear bucket, and speaker acoustic lid. The V2 script offers an ultra-compact footprint.
- **Nix Flake:** Fully reproducible development and build environment using Nix.

## Hardware Stack

- **Compute:** Raspberry Pi Zero W / 2W
- **Display:** 65x30mm I2C Display HAT
- **Audio:** Custom audio amplifier PCB & 32mm square speaker driver
- **Inputs:** Rotary Encoder (top) and two tactile plunge buttons (front)
- **Power:** USB-C Panel Mount / breakout

## Software Stack

- **Daemon:** Written in Go (`cmd/bedside`)
- **System Integration:** Systemd services and custom udev rules
- **CAD Automation:** Python (Fusion 360 API)

## Building

This project uses [Nix](https://nixos.org/) for development and builds.

```bash
# Enter the development environment
nix develop

# Build the Go binary
just build
```

## 3D Printing the Enclosure

The 3D printed case is generated procedurally using Fusion 360's Python API to ensure mathematical perfection and exact component tolerances.

1. Open Fusion 360
2. Go to **Utilities > Add-Ins > Scripts and Add-Ins**
3. Create a new script, or add the existing script from `hardware/fusion360/fusion360_setup_v2.py`.
4. Run the script. It will generate three solid bodies: `Front_Faceplate`, `Rear_Bucket`, and `Speaker_Acoustic_Lid`.
5. Right-click each body in the browser tree and select **Save As Mesh** (Export as 3MF, High Refinement, Millimeters).
6. Slice and print in Bambu Studio or your preferred slicer.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
