{ config, pkgs, lib, ... }:

{
  # We use the generic AArch64 SD image structure.
  sdImage = {
    # Disable automatic partition expansion on first boot because resize2fs of a 60GB
    # partition on a live rootfs causes the Pi Zero 2 W (512MB RAM) to OOM/freeze.
    expandOnBoot = false;
    
    # Give the FAT32 boot partition a better name (max 11 chars)
    firmwarePartitionName = "BEDSIDEBOOT";

    # The easiest way to apply the custom Pi boot config is to inject
    # the exact config.txt and firmware files into the FAT32 firmware partition.
    populateFirmwareCommands = lib.mkAfter ''
      # The nixos-hardware module runs first and creates firmware/config.txt
      # We append our custom configuration to it.
      chmod +w firmware/config.txt
      cat ${./boot/config.txt} >> firmware/config.txt

      cp ${./boot/panel.bin} firmware/panel.bin
    '';
  };
}
