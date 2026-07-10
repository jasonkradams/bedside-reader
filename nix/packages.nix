{ pkgs }:

let
  # Custom ffmpeg derivation, built from a pinned source tarball.
  # Stripped down to exactly what an Audible .aax -> .m4b stream copy
  # needs.
  ffmpeg = pkgs.stdenv.mkDerivation (finalAttrs: {
    pname = "ffmpeg-aax";
    version = "7.1.1";

    src = pkgs.fetchurl {
      url = "https://ffmpeg.org/releases/ffmpeg-${finalAttrs.version}.tar.xz";
      hash = "sha256-czmEOV4Nu+XARqvaLcSaVUTn4OHiNmu6hJIirp46A7E=";
    };

    strictDeps = true;

    configureFlags = [
      "--disable-everything"
      "--disable-autodetect"
      "--disable-network"
      "--disable-asm"
      "--disable-doc"
      "--disable-debug"
      "--disable-ffplay"
      "--disable-ffprobe"
      "--enable-demuxer=aax,mov"
      "--enable-protocol=file"
      "--enable-parser=aac,mjpeg"
      "--enable-decoder=aac,mjpeg"
      "--enable-muxer=ipod,mp4,mov"
      "--enable-bsf=aac_adtstoasc"
    ];

    configurePhase = ''
      runHook preConfigure
      ./configure --prefix="$out" --cc="$CC" \
        ${builtins.concatStringsSep " " finalAttrs.configureFlags}
      runHook postConfigure
    '';

    enableParallelBuilding = true;

    meta = {
      description = "Minimal ffmpeg that decrypts Audible .aax into .m4b";
      homepage = "https://ffmpeg.org/";
      license = pkgs.lib.licenses.lgpl21Plus;
      mainProgram = "ffmpeg";
    };
  });

  # Wraps the conversion loop and pins it to the ffmpeg above.
  audible-convert = pkgs.writeShellApplication {
    name = "audible-convert";
    runtimeInputs = [ ffmpeg ];
    text = ''
      # Decrypt every *.aax in a directory into *.m4b (lossless stream copy).
      dir="''${1:-.}"
      bytes="''${AUDIBLE_ACTIVATION_BYTES:-}"

      if [ -z "$bytes" ]; then
        echo "error: set AUDIBLE_ACTIVATION_BYTES (run: audible activation-bytes)" >&2
        exit 1
      fi
      if [ ! -d "$dir" ]; then
        echo "error: not a directory: $dir" >&2
        exit 1
      fi

      shopt -s nullglob
      found=0
      for f in "$dir"/*.aax; do
        found=1
        base="''${f%.aax}"
        base="''${base%_ep7}"
        out="$base.m4b"
        if [ -f "$out" ]; then
          echo "SKIP (exists): $out"
          continue
        fi
        echo "==> $f"
        ffmpeg -loglevel error -stats \
          -activation_bytes "$bytes" \
          -i "$f" -c copy -map_metadata 0 \
          "$out" -y
        echo "    done: $out"
      done

      if [ "$found" -eq 0 ]; then
        echo "no .aax files found in: $dir" >&2
        exit 1
      fi
    '';
  };

  # Override Go to 1.26.4 since Nixpkgs stable/unstable hasn't updated yet.
  # We fetch the exact source tarball from go.dev and override the package.
  go_1_26_4 = pkgs.go.overrideAttrs (_old: {
    version = "1.26.4";
    src = pkgs.fetchurl {
      url = "https://go.dev/dl/go1.26.4.src.tar.gz";
      hash = "sha256-T2aKMvv8ETLmqIH7lowvHa2mMUkqM5IRc1+7JVpCYC0=";
    };
  });

  # Re-bind buildGoModule to use our custom Go version
  buildGoModule_1_26_4 = pkgs.buildGoModule.override { go = go_1_26_4; };

  # Bedside App Go Binary
  bedside-app = buildGoModule_1_26_4 {
    pname = "bedside-app";
    version = "1.0.0";
    src = ../app; # Adjust path relative to this file
    vendorHash = "sha256-jJLJ/WK+YHIcg+N+Jvp6v6RHQxw/XxvXL5MIQbarZns=";
    ldflags = [
      "-s"
      "-w"
    ]; # strip DWARF + symbol table (image-size lever)
  };

  # Script to build the NixOS SD card image natively via Docker / Rancher Desktop / OrbStack
  build-os = pkgs.writeShellApplication {
    name = "build-os";
    text = ''
      echo "Building NixOS SD Card Image for AArch64 using Docker native Virtualization.framework..."

      # We use Docker to evaluate and build the NixOS image natively on the Linux VM.
      # This entirely bypasses the flaky macOS Nix daemon and QEMU HVF bugs.

      # Create a persistent volume for the Nix store so subsequent builds are fast
      docker volume create nixos-builder-store >/dev/null || true

      echo "Starting builder container (this may take a while to download/compile)..."
      docker run --rm \
        -v "$PWD":/workspace \
        -v nixos-builder-store:/nix \
        -w /workspace \
        nixos/nix:latest \
        bash -c "
          set -e

          # Run safe garbage collection. This removes old temporary files but strictly preserves
          # the base image (via its own roots) and our previous build's dependencies
          # (via the bedside-reader-latest root we create below).
          echo 'Cleaning up orphaned cache to prevent out-of-space errors...'
          nix-collect-garbage >/dev/null 2>&1 || true

          echo 'Building image...'
          nix build --out-link /nix/var/nix/gcroots/bedside-reader-latest --extra-experimental-features 'nix-command flakes' .#nixosConfigurations.bedside-reader.config.system.build.sdImage

          echo 'Copying image out of container...'
          mkdir -p result-img
          rm -rf result-img/*.img*
          cp -L /nix/var/nix/gcroots/bedside-reader-latest/sd-image/*.img* result-img/
          chmod 644 result-img/*.img*
        "

      echo "Done! Image is located at: ./result-img/"
    '';
  };

  # Script to flash the compiled NixOS image to an SD card (macOS only)
  flash-os = pkgs.writeShellApplication {
    name = "flash-os";
    runtimeInputs = [ pkgs.zstd ];
    text = ''
      DISK="''${1:-}"
      if [ -z "$DISK" ]; then
        echo "Scanning for physical disks..."
        # Find all physical disks except disk0 (main OS drive)
        mapfile -t DISKS < <(diskutil list | awk '/^\/dev\/disk[0-9]+ .*physical/ {print $1}' | grep -v '^/dev/disk0$')

        if [ ''${#DISKS[@]} -eq 0 ]; then
          echo "Error: No removable or external physical disks found!"
          echo "Please plug in your SD card."
          exit 1
        fi

        OPTIONS=()
        for d in "''${DISKS[@]}"; do
          SIZE=$(diskutil info "$d" | awk -F': +' '/Disk Size/ {print $2}' | cut -d'(' -f1 | xargs)
          NAME=$(diskutil info "$d" | awk -F': +' '/Device \/ Media Name/ {print $2}' | xargs)
          if [ -z "$NAME" ]; then
            NAME=$(diskutil info "$d" | awk -F': +' '/Device Identifier/ {print $2}' | xargs)
          fi
          OPTIONS+=("$d - $NAME ($SIZE)")
        done

        echo "Please select the target SD card to flash:"
        select opt in "''${OPTIONS[@]}" "Cancel"; do
          if [ "$opt" = "Cancel" ]; then
            echo "Cancelled."
            exit 0
          elif [ -n "$opt" ]; then
            DISK=$(echo "$opt" | awk '{print $1}')
            break
          else
            echo "Invalid selection. Please enter a number."
          fi
        done
      fi

      IMG=$(find result-img -name "nixos-image-*.img.zst" 2>/dev/null | head -n 1 || true)
      if [ -z "$IMG" ]; then
        echo "Error: No compressed image found in result-img/ directory."
        echo "Did you run 'build-os' first?"
        exit 1
      fi

      if df | grep -q "$DISK"; then
        echo "Warning: $DISK (or its partitions) is currently mounted."
        read -r -p "It must be unmounted to continue. Unmount now? [y/N] " confirm
        if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
          echo "Aborted."
          exit 1
        fi
      fi

      echo "Ensuring $DISK is unmounted..."
      diskutil unmountDisk "$DISK" >/dev/null 2>&1 || true

      RDISK="''${DISK/disk/rdisk}"
      echo "Flashing $IMG to $RDISK..."
      echo "This requires sudo privileges."
      zstdcat "$IMG" | sudo dd of="$RDISK" bs=1M
      sync

      # --- Provision Wi-Fi (kept out of git, the Nix store, and the image) ---
      # Inject your local, gitignored secrets/wireless.env onto the freshly
      # written boot partition so the device connects on first boot. Override
      # the path with BEDSIDE_WIRELESS_ENV if you keep it somewhere else.
      WIRELESS_ENV="''${BEDSIDE_WIRELESS_ENV:-secrets/wireless.env}"

      echo "Re-reading partition table and mounting the boot partition..."
      diskutil mountDisk "$DISK" >/dev/null 2>&1 || true
      sleep 2
      BOOT_MP="$(diskutil info "''${DISK}s1" 2>/dev/null | awk -F': +' '/Mount Point/ {print $2}' | sed 's/[[:space:]]*$//' || true)"
      if [ -z "$BOOT_MP" ]; then
        diskutil mount "''${DISK}s1" >/dev/null 2>&1 || true
        BOOT_MP="$(diskutil info "''${DISK}s1" 2>/dev/null | awk -F': +' '/Mount Point/ {print $2}' | sed 's/[[:space:]]*$//' || true)"
      fi

      if [ ! -f "$WIRELESS_ENV" ]; then
        echo "WARNING: no $WIRELESS_ENV found - the card will boot WITHOUT Wi-Fi."
        echo "         cp secrets/wireless.env.example secrets/wireless.env, fill it in, then re-run."
      elif [ -z "$BOOT_MP" ]; then
        echo "WARNING: could not mount ''${DISK}s1 to inject Wi-Fi."
        echo "         Copy $WIRELESS_ENV onto the BEDSIDEBOOT partition manually before booting."
      else
        cp "$WIRELESS_ENV" "$BOOT_MP/wireless.env"
        echo "Wi-Fi credentials written to $BOOT_MP/wireless.env"
      fi

      echo "Ejecting $DISK..."
      diskutil eject "$DISK" >/dev/null 2>&1 || true
      echo "Done! Move the card to the Pi and boot."
    '';
  };
  # Script to natively cross-compile updates via Docker and deploy over SSH
  deploy-os = pkgs.writeShellApplication {
    name = "deploy-os";
    text = ''
      echo "Deploying updates to Pi natively via Docker Virtualization.framework..."

      # Generate a temporary SSH key for Docker to use
      rm -rf .tmp-ssh
      mkdir -p .tmp-ssh
      ssh-keygen -t ed25519 -f .tmp-ssh/id_ed25519 -N "" -q

      # Authorize it on the Pi using macOS native SSH (which uses your agent/config)
      echo "Authorizing temporary Docker SSH key on the Pi..."
      cat .tmp-ssh/id_ed25519.pub | ssh root@10.136.249.149 "mkdir -p ~/.ssh; cat >> ~/.ssh/authorized_keys"

      # Create a persistent volume for the Nix store so subsequent builds are fast
      docker volume create nixos-builder-store >/dev/null || true

      echo "Starting builder container..."
      docker run --rm \
        -v "$PWD":/workspace \
        -v nixos-builder-store:/nix \
        -v "$PWD/.tmp-ssh":/tmp/ssh:ro \
        -w /workspace \
        nixos/nix:latest \
        bash -c "
          set -e

          # The repo is bind-mounted from the host and owned by the host user, not
          # container root. Without trusting it, git reports 'dubious ownership', and
          # nix silently falls back to a STALE cached flake source in the persistent
          # /nix volume — deploying an old config no matter what you changed. Trusting
          # the workdir makes nix evaluate the real current HEAD.
          git config --global --add safe.directory '*'

          mkdir -p /root/.ssh
          cp /tmp/ssh/id_ed25519 /root/.ssh/
          chmod 600 /root/.ssh/id_ed25519

          echo 'Host *' > /root/.ssh/config
          echo '  StrictHostKeyChecking accept-new' >> /root/.ssh/config
          echo '  IdentityFile /root/.ssh/id_ed25519' >> /root/.ssh/config
          chmod 600 /root/.ssh/config

          # NOTE: intentionally no nix-collect-garbage here. It deleted the RPi
          # kernel from the persistent volume on every run, forcing a ~20min kernel
          # rebuild each deploy. The volume persists across deploys; if it ever grows
          # too large, prune it explicitly with 'docker volume rm nixos-builder-store'.

          echo 'Running colmena apply...'
          nix run --extra-experimental-features 'nix-command flakes' nixpkgs#colmena apply
        "

      # Cleanup
      rm -rf .tmp-ssh
      echo "Done!"
    '';
  };

in
{
  inherit
    ffmpeg
    audible-convert
    bedside-app
    build-os
    flash-os
    deploy-os
    go_1_26_4
    ;
  default = audible-convert;
}
