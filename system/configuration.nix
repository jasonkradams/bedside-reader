{
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
  boot = {
    # Disable extremely heavy filesystems like ZFS and Btrfs which are enabled by default
    # in NixOS. Loading ZFS modules and daemons on a 512MB Pi Zero causes instant OOM freezes!
    supportedFilesystems = lib.mkForce [ "vfat" "ext4" ];

    # DIAGNOSTIC (2026-07-02): the board has never mounted root — it hangs in the
    # initrd before anything reaches disk (root fs shows Last-mount-time n/a, all
    # logs empty). Fall back from systemd-initrd to the scripted initrd, which
    # resolves the SD root more reliably on Raspberry Pi, to test that hypothesis.
    # Revert once the real cause is known.
    initrd.systemd.enable = lib.mkForce false;
  };

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
      cp ${../boot/wireless.env.example} firmware/wireless.env.example
    '';
  };

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

  # Install required packages
  environment.systemPackages = [
    bedside-app
    pkgs.mpv
  ];

  # Ensure the bedside user and required groups exist
  users = {
    groups.bedside = { };
    groups.gpio = { };
    
    users.bedside = {
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
    
    # Allow ssh access for deployment
    users.root.openssh.authorizedKeys.keys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFi9PGouQ/5XIMO6NSUzOqVPkPBFR9DmWDOjF1CZ96Io"
    ];
  };

  # ---------------------------------------------------------
  # Networking & Wi-Fi
  # ---------------------------------------------------------

  networking = {
    wireless = {
      enable = true;
      interfaces = [ "wlan0" ];
      # By NOT defining `networks`, NixOS automatically enables imperative mode
      # which provisions `/etc/wpa_supplicant/imperative.conf` and uses it as the
      # primary config, bypassing any ReadOnlyPaths mount namespace issues.
    };
    # Use DHCP on wlan0 once connected
    interfaces.wlan0.useDHCP = true;
  };

  services = {
    # Flush logs to the SD card every 1 second (default is 5 minutes).
    # This prevents logs from being lost if the device hangs and is unplugged prematurely.
    journald.extraConfig = "SyncIntervalSec=1";
    
    # Udev rules
    udev.extraRules = ''
      # Allow the video group to adjust backlight brightness
      SUBSYSTEM=="backlight", ACTION=="add", \
        RUN+="${pkgs.coreutils}/bin/chgrp video /sys/class/backlight/%k/brightness", \
        RUN+="${pkgs.coreutils}/bin/chmod g+w /sys/class/backlight/%k/brightness"

      # Allow the gpio group to access gpiochip devices
      SUBSYSTEM=="gpio", KERNEL=="gpiochip*", ACTION=="add", \
        RUN+="${pkgs.coreutils}/bin/chgrp gpio /dev/%k", \
        RUN+="${pkgs.coreutils}/bin/chmod g+rw /dev/%k"
    '';

    # Allow ssh access for deployment
    openssh.enable = true;
  };

  # ---------------------------------------------------------
  # Systemd Services
  # ---------------------------------------------------------

  systemd = {
    services = {
      # We read the user's wireless.env file from the FAT32 boot partition
      # to dynamically configure Wi-Fi without hardcoding credentials in Nix.
      extract-wifi-credentials = {
        description = "Extract Wi-Fi credentials from FAT32 boot partition";
        before = [ "wpa_supplicant-wlan0.service" ];
        wantedBy = [ "multi-user.target" ];
        serviceConfig = {
          Type = "oneshot";
          RemainAfterExit = true;
        };
        script = ''
          # Always ensure the file exists so wpa_supplicant doesn't crash on boot
          touch /var/lib/wifi.conf

          mkdir -p /tmp/firmware_tmp
          mount /dev/mmcblk0p1 /tmp/firmware_tmp || true
          if [ -f /tmp/firmware_tmp/wireless.env ]; then
            # shellcheck source=/dev/null
            source /tmp/firmware_tmp/wireless.env
            if [ -n "''${WIFI_SSID:-}" ]; then
              printf "ctrl_interface=/run/wpa_supplicant\nnetwork={\n  ssid=\"%s\"\n  psk=\"%s\"\n}\n" "''${WIFI_SSID}" "''${WIFI_PASSWORD:-}" > /var/lib/wifi.conf
            fi
          fi
          umount /tmp/firmware_tmp || true
        '';
      };

      # Override wpa_supplicant to use our explicitly extracted config file
      "wpa_supplicant-wlan0" = {
        wants = [ "extract-wifi-credentials.service" ];
        serviceConfig.ExecStart = lib.mkForce "${pkgs.wpa_supplicant}/sbin/wpa_supplicant -c /var/lib/wifi.conf -i wlan0";
      };

      # Bedside Go Backend Service
      bedside = {
        description = "Bedside Audiobook Player";
        after = [ "sound.target" ];
        wantedBy = [ "multi-user.target" ];
        
        path = [ pkgs.mpv ];

        serviceConfig = {
          Type = "notify";
          # Export GPIO 13 (backlight) as root before starting, and make it writable by nobody.
          # If sysfs fails (e.g. pin already claimed by kernel pinctrl), we ignore it and continue.
          ExecStartPre = [
            "+${pkgs.bash}/bin/bash -c 'if [ ! -d /sys/class/gpio/gpio13 ]; then echo 13 > /sys/class/gpio/export || true; sleep 0.1; fi; echo out > /sys/class/gpio/gpio13/direction || true; chown nobody:video /sys/class/gpio/gpio13/value || true'"
          ];
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
    };
  };
}
