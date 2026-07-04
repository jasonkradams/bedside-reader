# Image-size optimizations for the bedside appliance.
#
# Imported by configuration.nix so BOTH the SD image (build-os/flash-os) AND
# incremental deploys (deploy-os/colmena) get the identical slimmed closure.
# Rationale, measured baselines, and per-lever savings live in
# docs/nixos_image_optimization.md.
#
# Only pure module-option levers live here. Two levers need reference changes and
# live at their reference sites instead (kept out of here on purpose):
#   - audio-only mpv  -> configuration.nix (swaps the two `pkgs.mpv` references;
#     an overlay here would be IGNORED on the colmena path, which sets nixpkgs.pkgs
#     externally — that would silently diverge the image from the deploy).
#   - Go `-ldflags -s -w` -> nix/packages.nix (on the bedside-app derivation).
{ config, pkgs, lib, ... }:

{
  # Lever 8 (~197MB): the flake's whole nixpkgs source is embedded in the closure
  # via /etc/nix/registry.json + $NIX_PATH so on-device `nix`/nixos-rebuild can
  # resolve <nixpkgs>. This appliance is only ever built/deployed remotely
  # (build-os / deploy-os / colmena), never rebuilt on the Pi, so drop it.
  nixpkgs.flake.setFlakeRegistry = false;
  nixpkgs.flake.setNixPath = false;

  # Lever 9 (~120MB): profiles/base.nix (pulled in ONLY by the sd-image module)
  # adds rescue/installer utilities — vim, testdisk, parted, gptfdisk, ddrescue,
  # cryptsetup, tcpdump, smartmontools, hdparm, sdparm, pciutils, usbutils,
  # nvme-cli, w3m, ... none of which a sealed audiobook appliance needs. Removing
  # it also makes the flashed image match the (base-less) colmena deploy closure.
  # base.nix also set networking.hostId (ZFS-only; unused here) and
  # boot.supportedFilesystems (already mkForce'd to vfat+ext4 in configuration.nix).
  disabledModules = [ "profiles/base.nix" ];

  # Lever 10: drop the on-device installer toolchain (nixos-install,
  # nixos-generate-config, nixos-enter, ...) — deployment is remote.
  system.disableInstallerTools = true;
  # Lever 1 (~762MB uncompressed): the redistributable linux-firmware tree
  # (AMD/Intel/Realtek/Mellanox blobs) is the single largest thing in the closure
  # and none of it applies to this board. all-hardware.nix sets
  # enableRedistributableFirmware=true at normal priority, so mkForce is required.
  # The Broadcom Wi-Fi blob survives ONLY because configuration.nix lists
  # pkgs.raspberrypiWirelessFirmware explicitly in hardware.firmware — never drop
  # that line or wlan0 stops getting its firmware (brcmfmac43430b0-sdio.bin, -2).
  hardware.enableRedistributableFirmware = lib.mkForce false;
  # regdb defaults to the same flag and is dropped with it; harmless on a
  # 2.4GHz-only Pi Zero 2 W. Re-enable if 5GHz hardware is ever added.
  hardware.wirelessRegulatoryDatabase = lib.mkForce false;

  # Lever 3 (~50-150MB est): headless appliance — nothing on-device reads
  # man/info/HTML. The master switch gates the rest; the sub-options are
  # belt-and-suspenders. (Does NOT free perl or w3m — see the doc.)
  documentation.enable = false;
  documentation.nixos.enable = false;
  documentation.man.enable = false;
  documentation.doc.enable = false;
  documentation.info.enable = false;

  # Lever 5 (small): the default env set is only [ perl rsync strace ]; perl (~50MB)
  # drops because switch-to-configuration-ng (Rust) is the activation tool.
  environment.defaultPackages = lib.mkForce [ ];
}
