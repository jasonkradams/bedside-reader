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

  # Ensure the bedside user and group exist
  users.groups.bedside = { };
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
    pkgs.cog
  ];

  # ---------------------------------------------------------
  # Networking & Wi-Fi
  # ---------------------------------------------------------

  networking.wireless.enable = true;

  # We read the user's wireless.env file from the FAT32 boot partition
  # to dynamically configure Wi-Fi without hardcoding credentials in Nix.
  systemd.services.wpa_supplicant.preStart = lib.mkBefore ''
    if [ -f /boot/firmware/wireless.env ]; then
      # Source the environment variables safely
      source /boot/firmware/wireless.env
      # Generate the wpa_supplicant configuration block
      printf "network={\n  ssid=\"%s\"\n  psk=\"%s\"\n}\n" "$WIFI_SSID" "$WIFI_PASSWORD" > /tmp/wireless.conf
    else
      # If no file exists, create an empty one so the include doesn't crash
      touch /tmp/wireless.conf
    fi
  '';

  # Tell wpa_supplicant to include our dynamically generated config
  networking.wireless.extraConfig = "include /tmp/wireless.conf";

  # ---------------------------------------------------------
  # Systemd Services
  # ---------------------------------------------------------

  # Udev rules
  services.udev.extraRules = ''
    # Allow the video group to adjust backlight brightness
    SUBSYSTEM=="backlight", ACTION=="add", \
      RUN+="${pkgs.coreutils}/bin/chgrp video /sys/class/backlight/%k/brightness", \
      RUN+="${pkgs.coreutils}/bin/chmod g+w /sys/class/backlight/%k/brightness"
  '';

  # Bedside Go Backend Service
  systemd.services.bedside = {
    description = "Bedside Audiobook Player";
    after = [
      "network-online.target"
      "sound.target"
    ];
    wants = [ "network-online.target" ];
    wantedBy = [ "multi-user.target" ];

    serviceConfig = {
      Type = "notify";
      ExecStart = "${bedside-app}/bin/bedside";
      Restart = "always";
      RestartSec = 2;
      User = "bedside";
      Group = "bedside";
      SupplementaryGroups = "audio gpio video";
      StateDirectory = "bedside";
      ReadWritePaths = "/var/lib/bedside";
      ProtectSystem = "strict";
      ProtectHome = true;
      NoNewPrivileges = true;
      WatchdogSec = 30;
    };
  };

  # Cog Kiosk Frontend Service
  systemd.services.cog = {
    description = "Bedside Kiosk (Cog on KMS/DRM)";
    after = [
      "bedside.service"
      "systemd-user-sessions.service"
    ];
    requires = [ "bedside.service" ];
    wantedBy = [ "multi-user.target" ];

    # Only start if the DRM card exists (Wait for VC4 driver)
    unitConfig.ConditionPathExists = "/dev/dri/card0";

    environment = {
      WEBKIT_DISABLE_COMPOSITING_MODE = "0";
      XDG_RUNTIME_DIR = "/run/user/%U";
    };

    serviceConfig = {
      Type = "simple";
      User = "bedside";
      Group = "bedside";
      SupplementaryGroups = "video render input";

      # Wait for the backend API to be healthy before starting Cog
      ExecStartPre = "${pkgs.bash}/bin/sh -c 'until ${pkgs.curl}/bin/curl -fsS http://localhost:8080/healthz; do sleep 0.3; done'";

      ExecStart = ''
        ${pkgs.cog}/bin/cog \
          --platform=drm \
          --geometry=320x240 \
          --bg-color=000000 \
          http://localhost:8080
      '';

      Restart = "always";
      RestartSec = 2;
    };
  };

  # Allow ssh access for deployment
  services.openssh.enable = true;
  users.users.root.openssh.authorizedKeys.keys = [
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFi9PGouQ/5XIMO6NSUzOqVPkPBFR9DmWDOjF1CZ96Io"
  ];
}
