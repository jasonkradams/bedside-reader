# OS Bootstrapping Guide

This runbook describes how to compile the completely declarative NixOS operating system from scratch for the Raspberry Pi Zero 2 W using macOS, and how to successfully flash it to an SD card.

## 1. Prerequisites

Because building the Linux kernel and the NixOS SD card image requires Linux-specific filesystems and features, macOS users must compile the OS using a lightweight Linux virtual machine.

This project relies on **Docker** (or Rancher Desktop / OrbStack) leveraging Apple's `Virtualization.framework` to natively and cleanly evaluate the build.

**CRITICAL: Allocate Sufficient Resources**
Building the Linux kernel from source is extremely CPU and memory intensive. By default, Rancher Desktop only provides 2 CPU cores and 4 GB of RAM, which will make the build take over an hour.
1. Open your Docker provider's Preferences (e.g., Rancher Desktop Settings).
2. Navigate to the **Virtual Machine** tab.
3. Allocate at least **10-12 CPUs** (leaving 2-4 cores for macOS) and at least **16 GB of RAM**.
4. Apply and wait for the VM to restart.

## 2. Compile the SD Card Image

Once your environment is configured, execute the build command provided by the development shell:

```bash
build-os
```

### What happens under the hood?
1. The script creates a persistent docker volume (`nixos-builder-store`) so that future builds cache the kernel and only re-compile what has changed.
2. It launches an ephemeral `nixos/nix:latest` container and mounts the current source code directory.
3. It evaluates `flake.nix` for the `bedside-pi` configuration.
4. It natively cross-compiles the entire operating system, including the custom `linux-rpi` kernel and our Go daemon (`bedside-app`).
5. It extracts the resulting `.img.zst` file into the local `./result-img/` directory.

## 3. Flashing the SD Card

Once the image is built, you must flash it to your SD card. We have provided a custom script that safely unmounts the SD card, decompresses the Zstandard image on the fly, and uses `dd` to write directly to the raw block device for maximum speed.

1. **Identify your SD Card Disk ID**
   Run the following command to find your SD card's disk identifier (e.g., `/dev/disk12`):
   ```bash
   diskutil list
   ```

2. **Flash the OS**
   Pass your exact disk identifier to the flashing script:
   ```bash
   flash-os /dev/disk12
   ```

You will be prompted for your macOS `sudo` password to allow the raw block write.

## 4. Booting the Pi

Once the flashing script completes successfully, eject the SD card and insert it into your Raspberry Pi Zero 2 W. 

Upon providing power, the Pi will boot. Note that the **first boot** takes several minutes because NixOS will automatically expand the root filesystem to fill the remainder of the SD card and securely generate the SSH host keys.

Once complete, it will connect to the Wi-Fi network specified in your `system/configuration.nix` file and the `bedside` audiobook Go daemon will launch automatically.

## 5. Subsequent Updates

You **do not** need to rebuild the entire SD card to deploy updates to the Go application or system configuration!

Once the Pi is online, you can use the `deploy-os` script to evaluate, incrementally compile, and push new Go binaries and configuration changes directly over SSH in seconds. This script runs `colmena apply` natively inside the Docker Linux VM, completely bypassing macOS cross-compilation errors:

```bash
deploy-os
```

This will automatically connect to the Pi as `root`, build the closure, and activate the new system state without requiring a reboot.
