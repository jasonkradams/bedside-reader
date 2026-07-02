{
  config,
  pkgs,
  lib,
  bedside-app,
  ...
}:

{
  # Set the release version
  system.stateVersion = "24.05";

  # ---------------------------------------------------------
  # Hardware & Boot
  # ---------------------------------------------------------

  # We use the generic AArch64 SD image structure.
  # Disable automatic partition expansion on first boot because resize2fs of a 60GB
  # partition on a live rootfs causes the Pi Zero 2 W (512MB RAM) to OOM/freeze.
  sdImage.expandOnBoot = false;
  
  # Give the FAT32 boot partition a better name (max 11 chars)
  sdImage.firmwarePartitionName = "BEDSIDEBOOT";
  
  # The easiest way to apply the custom Pi boot config is to inject
  # the exact config.txt and firmware files into the FAT32 firmware partition.
  sdImage.populateFirmwareCommands = lib.mkAfter ''
    # The nixos-hardware module runs first and creates firmware/config.txt
    # We append our custom configuration to it.
    chmod +w firmware/config.txt
    cat ${./boot/config.txt} >> firmware/config.txt

    cp ${./boot/panel.bin} firmware/panel.bin
    cp ${./boot/user-data} firmware/user-data
    cp ${../boot/wireless.env.example} firmware/wireless.env.example
  '';

  # The panel.bin firmware must also be available in the root filesystem
  # so the Linux kernel can load it when the ST7789V driver requests it.
  hardware.firmware = [
    (pkgs.runCommand "display-firmware" { } ''
      mkdir -p $out/lib/firmware
      cp ${./boot/panel.bin} $out/lib/firmware/panel.bin
    '')
  ];

  # ---------------------------------------------------------
  # Packages & Services
  # ---------------------------------------------------------

  # Ensure the bedside user and required groups exist
  users.groups.bedside = { };
  users.groups.gpio = { };
  users.users.bedside = {
    isNormalUser = true;
    group = "bedside";
    extraGroups = [
      "audio"
      "gpio"
      "video"
      "render"
      "input"
    ];
  };

  # Install required packages
  environment.systemPackages = [
    bedside-app
    pkgs.mpv
  ];

  # ---------------------------------------------------------
  # Networking & Wi-Fi
  # ---------------------------------------------------------

  networking.wireless = {
    enable = true;
    interfaces = [ "wlan0" ];
    # Define a dummy network so NixOS generates the wpa_supplicant systemd service.
    # Our preStart script below will inject the real configuration dynamically.
    networks = {
      "dummy-force-service-creation" = {};
    };
  };

  # Use DHCP on wlan0 once connected
  networking.interfaces.wlan0.useDHCP = true;

  # We read the user's wireless.env file from the FAT32 boot partition
  # to dynamically configure Wi-Fi without hardcoding credentials in Nix.
  systemd.services."wpa_supplicant-wlan0".preStart = lib.mkBefore ''
    if [ -f /boot/firmware/wireless.env ]; then
      # Source the environment variables safely
      # shellcheck source=/dev/null
      source /boot/firmware/wireless.env
      # Generate the wpa_supplicant configuration block
      printf "network={\n  ssid=\"%s\"\n  psk=\"%s\"\n}\n" "''${WIFI_SSID:-}" "''${WIFI_PASSWORD:-}" > /run/wireless.conf
    else
      # If no file exists, create an empty one so the include doesn't crash
      touch /run/wireless.conf
    fi
  '';

  # Tell wpa_supplicant to load our dynamically generated config
  networking.wireless.extraConfigFiles = [ "/run/wireless.conf" ];

  # ---------------------------------------------------------
  # Systemd Services
  # ---------------------------------------------------------

  # Udev rules
  services.udev.extraRules = ''
    # Allow the video group to adjust backlight brightness
    SUBSYSTEM=="backlight", ACTION=="add", \
      RUN+="${pkgs.coreutils}/bin/chgrp video /sys/class/backlight/%k/brightness", \
      RUN+="${pkgs.coreutils}/bin/chmod g+w /sys/class/backlight/%k/brightness"

    # Allow the gpio group to access gpiochip devices
    SUBSYSTEM=="gpio", KERNEL=="gpiochip*", ACTION=="add", \
      RUN+="${pkgs.coreutils}/bin/chgrp gpio /dev/%k", \
      RUN+="${pkgs.coreutils}/bin/chmod g+rw /dev/%k"
  '';

  # Bedside Go Backend Service
  systemd.services.bedside = {
    description = "Bedside Audiobook Player";
    after = [ "sound.target" ];
    wantedBy = [ "multi-user.target" ];
    
    path = [ pkgs.mpv ];

    serviceConfig = {
      Type = "notify";
      ExecStart = "${bedside-app}/bin/bedside";
      Restart = "always";
      RestartSec = 2;
      User = "bedside";
      Group = "bedside";
      StateDirectory = "bedside";
      ReadWritePaths = "/var/lib/bedside";
      ProtectSystem = "strict";
      ProtectHome = true;
      NoNewPrivileges = true;
      WatchdogSec = 30;
    };
  };

  # Allow ssh access for deployment
  services.openssh.enable = true;
  users.users.root.openssh.authorizedKeys.keys = [
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFi9PGouQ/5XIMO6NSUzOqVPkPBFR9DmWDOjF1CZ96Io"
  ];
}
