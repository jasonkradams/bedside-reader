# NixOS Image Optimization Inventory

A 1.3GB compressed `.img.zst` means your uncompressed OS image is likely over 3-4GB. For a single-purpose kiosk appliance on a Raspberry Pi Zero 2 W, this is definitely bloated!

Because NixOS is built declaratively, we have incredible control over exactly what gets included in the closure. Here is a thorough inventory of the largest offenders and how we can strip them out to potentially bring the image size down to the 200-400MB range.

## 1. The Massive Firmware Bundle (`linux-firmware`)
**Estimated Savings:** ~500MB+ (Uncompressed)
By default, NixOS sets `hardware.enableRedistributableFirmware = true;` to ensure Wi-Fi and Bluetooth work out of the box on all devices. However, this pulls in the entire Linux firmware tree, including gigabytes of binaries for AMD Radeon GPUs, Intel Wi-Fi cards, and enterprise RAID controllers.
**The Fix:** Disable it, and explicitly inject *only* the Broadcom `brcmfmac` firmware needed for the Pi Zero 2 W's Wi-Fi chip.

## 2. The Universal Kernel & Modules
**Estimated Savings:** ~300MB
The generic `aarch64-linux` kernel in NixOS is compiled with thousands of modules to support every ARM server, laptop, and SBC on the market.
**The Fix:** We can switch from the generic `linuxPackages` to a specialized Raspberry Pi kernel (`linuxPackages_rpi02w` or similar), or use `boot.initrd.includeDefaultModules = false;` to strip out drivers for things like NVMe drives, USB Ethernet adapters, and graphics cards we don't own.

## 3. Documentation & Man Pages
**Estimated Savings:** ~150-200MB
NixOS includes the full NixOS manual, man pages, info pages, and developer documentation in the base closure by default.
**The Fix:** For a headless/kiosk appliance, we don't need docs.
```nix
documentation.enable = false;
documentation.nixos.enable = false;
```

## 4. WebKitGTK / WPEWebKit Bloat
**Estimated Savings:** ~200MB
We are using `cog` to render the UI via DRM/KMS. However, `cog` and `wpewebkit` might be pulled in with support for Wayland, X11, and massive rendering backends like Mesa, libDRM, GStreamer, ICU, and HarfBuzz.
**The Fix:** Set `environment.noXlibs = true;` to globally ban X11 libraries from the system. If `wpewebkit` still pulls in too much, we can override its derivation to explicitly disable Wayland and X11 support (`-DENABLE_WAYLAND_TARGET=OFF`), compiling it *only* for raw KMS/DRM.

## 5. Unused Base Packages & Services
**Estimated Savings:** ~100MB
NixOS includes a set of base CLI tools and services assuming a human will be logging in (e.g., `perl`, `rsync`, `strace`, `nano`, `lvm2`, `udisks2`).
**The Fix:**
```nix
environment.defaultPackages = [ ];
services.udisks2.enable = false;
boot.enableContainers = false;
```

## 6. Audio/Media Server Bloat
We only need raw ALSA to output I2S audio to the MAX98357A amp. If NixOS is accidentally pulling in PipeWire, WirePlumber, PulseAudio, or `rtkit`, we can explicitly disable them and rely purely on `alsa-utils`.

## 7. Go Binary Debug Symbols
**Estimated Savings:** ~5-15MB
The `bedside` Go application is currently built with default flags.
**The Fix:** We can pass `-ldflags="-s -w"` in the `buildGoModule` derivation to strip the DWARF debug symbols and reduce the binary size.

---

### Summary
If we apply all of these optimizations, we could likely shrink the compressed image from 1.3GB down to around **150MB - 300MB**. Because everything in Nix is a graph of dependencies, cutting out `linux-firmware` and `X11` will aggressively prune the tree.

*Note: Since you requested an inventory only, no changes have been made to `configuration.nix`.*
