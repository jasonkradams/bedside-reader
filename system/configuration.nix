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
  boot = {
    # Disable extremely heavy filesystems like ZFS and Btrfs which are enabled by default
    # in NixOS. Loading ZFS modules and daemons on a 512MB Pi Zero causes instant OOM freezes!
    supportedFilesystems = lib.mkForce [ "vfat" "ext4" ];

    # CONFIRMED FIX: systemd-initrd could not mount the SD root on this Pi Zero 2 W,
    # because the specific SD host controller drivers were missing from stage 1.
    # By adding them explicitly, systemd-initrd works perfectly.
    initrd.availableKernelModules = [
      "sdhci_bcm2835"
      "sdhci_iproc"
      "bcm2835_dma"
      "i2c_bcm2835"
    ];
  };


  # Enable ZRAM so we have enough memory to run resize2fs on large SD cards
  zramSwap = {
    enable = true;
    memoryPercent = 150; # 150% of 512MB RAM ~ 750MB compressed swap
  };

  fileSystems."/" = {
    device = "/dev/disk/by-label/NIXOS_SD";
    fsType = "ext4";
  };



  # The panel.bin firmware must also be available in the root filesystem
  # so the Linux kernel can load it when the ST7789V driver requests it.
  hardware.firmware = [
    # Broadcom BCM43430 Wi-Fi + Bluetooth firmware for the Pi Zero 2 W. The
    # downstream linux-rpi driver requests brcm/brcmfmac43430b0-sdio.bin plus the
    # board-specific nvram, which the generic redistributable linux-firmware does
    # not reliably ship — without this, wlan0 never appears (firmware load -2).
    pkgs.raspberrypiWirelessFirmware

    (pkgs.runCommand "display-firmware" { } ''
      mkdir -p $out/lib/firmware
      cp ${./boot/panel.bin} $out/lib/firmware/panel.bin
    '')
  ];

  # Apply device-tree overlays into the kernel DTB at build time. The config.txt
  # dtoverlays never reach the kernel here: populateFirmwareCommands is image-build
  # only (so colmena/nixos-rebuild deploys don't carry them), and U-Boot's FDTDIR
  # loads NixOS's own DTB and discards anything the firmware assembled. Describing
  # the appliance hardware here is what actually takes effect.
  hardware.deviceTree = {
    # Only the Zero 2 W board DTB needs our overlays; applying to every DTB in the
    # set risks fdtoverlay failing on one that lacks a targeted symbol.
    filter = "bcm2710-rpi-zero-2-w.dtb";
    overlays = [
      {
        # MAX98357A I2S amplifier. The prebuilt max98357a/hifiberry-dac overlays only
        # enable I2S under fdtoverlay — their card setup is parameter-gated and needs
        # the RPi firmware's overlay applicator. So describe the card explicitly:
        # simple-audio-card (snd-soc-simple-card) wiring the I2S controller to the
        # maxim,max98357a codec (snd-soc-max98357a); both drivers ship in this kernel.
        # No sdmode-gpios: the amp's SD_MODE is tied on in hardware.
        name = "max98357a-simple";
        dtsText = ''
          /dts-v1/;
          /plugin/;
          / {
            # Must intersect the base dtb's compatible ("raspberrypi,model-zero-2-w",
            # "brcm,bcm2837"), or NixOS's apply_overlays.py silently skips the overlay.
            # (The Zero 2 W is BCM2837, not the BCM2835 the prebuilt RPi overlays assume.)
            compatible = "brcm,bcm2837";

            fragment@0 {
              target = <&i2s_clk_producer>;
              __overlay__ {
                status = "okay";
              };
            };

            fragment@1 {
              target-path = "/";
              __overlay__ {
                max98357a_codec: max98357a {
                  #sound-dai-cells = <0>;
                  compatible = "maxim,max98357a";
                  status = "okay";
                };
              };
            };

            fragment@2 {
              target = <&sound>;
              __overlay__ {
                compatible = "simple-audio-card";
                simple-audio-card,name = "MAX98357A";
                status = "okay";
                simple-audio-card,cpu {
                  sound-dai = <&i2s_clk_producer>;
                };
                simple-audio-card,codec {
                  sound-dai = <&max98357a_codec>;
                };
              };
            };
          };
        '';
      }
    ];
  };

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

    # Safety net for the ~399MB-usable board: kill the biggest memory hog before a
    # runaway (e.g. an accidental on-device build) thrashes RAM + zram into an
    # unresponsive state that needs a physical power-cycle. earlyoom is a tiny
    # mlock'd daemon that stays responsive under pressure and keeps sshd alive.
    earlyoom = {
      enable = true;
      freeMemThreshold = 10; # SIGTERM the worst process under ~10% free RAM
      freeSwapThreshold = 20; # ...once zram swap is also mostly full
      reportInterval = 0; # no periodic memory-report spam in the journal
    };
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
        # mount/umount live in util-linux. A systemd unit's PATH does not include
        # them by default, so without this the mount silently fails ("command not
        # found") and wireless.env is never read — wifi can never come up.
        path = [ pkgs.util-linux ];
        serviceConfig = {
          Type = "oneshot";
          RemainAfterExit = true;
        };
        script = ''
          # Always leave a syntactically valid config behind so wpa_supplicant
          # starts cleanly even before/without credentials on the card.
          mkdir -p /etc/wpa_supplicant
          printf 'ctrl_interface=/run/wpa_supplicant\nupdate_config=1\n' > /etc/wpa_supplicant/wifi.conf

          mkdir -p /tmp/firmware_tmp
          mount /dev/disk/by-label/BEDSIDEBOOT /tmp/firmware_tmp || true
          if [ -f /tmp/firmware_tmp/wireless.env ]; then
            # shellcheck source=/dev/null
            source /tmp/firmware_tmp/wireless.env
            if [ -n "''${WIFI_SSID:-}" ]; then
              printf "ctrl_interface=/run/wpa_supplicant\nnetwork={\n  ssid=\"%s\"\n  psk=\"%s\"\n}\n" "''${WIFI_SSID}" "''${WIFI_PASSWORD:-}" > /etc/wpa_supplicant/wifi.conf
            fi
          fi
          umount /tmp/firmware_tmp || true
        '';
      };

      # Override wpa_supplicant to use our explicitly extracted config file.
      "wpa_supplicant-wlan0" = {
        # `after` (not just `wants`) so wpa waits for the config to be written;
        # the log showed it starting before extract-wifi-credentials had run.
        wants = [ "extract-wifi-credentials.service" ];
        after = [ "extract-wifi-credentials.service" ];
        serviceConfig = {
          ExecStart = lib.mkForce "${pkgs.wpa_supplicant}/sbin/wpa_supplicant -c /etc/wpa_supplicant/wifi.conf -i wlan0";
          # A single failed start otherwise leaves wifi down for the whole boot.
          Restart = lib.mkForce "on-failure";
          RestartSec = 3;
        };
      };

      # Bedside Go Backend Service
      bedside = {
        description = "Bedside Audiobook Player";
        after = [ "sound.target" ];
        wantedBy = [ "multi-user.target" ];
        
        # mpv plays; ffmpeg-headless provides ffprobe, which the app shells out to
        # for duration/chapter metadata (the project's custom ffmpeg omits ffprobe).
        path = [ pkgs.mpv pkgs.ffmpeg-headless ];

        serviceConfig = {
          Type = "notify";
          ExecStartPre = [
            "+${pkgs.bash}/bin/bash -c 'if [ ! -d /sys/class/gpio/gpio525 ]; then echo 525 > /sys/class/gpio/export; fi'"
            "+-${pkgs.coreutils}/bin/chgrp -R gpio /sys/class/gpio/gpio525/"
            "+-${pkgs.coreutils}/bin/chmod -R g+rw /sys/class/gpio/gpio525/"
          ];
          ExecStart = "${bedside-app}/bin/bedside";
          Restart = "always";
          RestartSec = 2;
          User = "bedside";
          Group = "bedside";
          SupplementaryGroups = [ "video" "input" "gpio" "audio" "render" ];
          StateDirectory = "bedside";
          ReadWritePaths = "/var/lib/bedside";
          # ProtectSystem = "strict";
          # ProtectHome = true;
          # NoNewPrivileges = true;
          WatchdogSec = 30;
        };
      };
    };
  };
}
