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

  # Script to stage boot files to the SD card
  stage-boot = pkgs.writeShellApplication {
    name = "stage-boot";
    text = ''
      boot_dir="''${1:-/Volumes/bootfs}"
      if [ ! -d "$boot_dir" ]; then
        echo "Error: Directory '$boot_dir' does not exist." >&2
        exit 1
      fi
      cp -v system/boot/config.txt "$boot_dir/config.txt"
      cp -v system/boot/cmdline.txt "$boot_dir/cmdline.txt"
      cp -v system/boot/user-data "$boot_dir/user-data"
      cp -v system/boot/panel.bin "$boot_dir/panel.bin"
    '';
  };

  # Override Go to 1.26.4 since Nixpkgs stable/unstable hasn't updated yet.
  # We fetch the exact source tarball from go.dev and override the package.
  go_1_26_4 = pkgs.go.overrideAttrs (old: {
    version = "1.26.4";
    src = pkgs.fetchurl {
      url = "https://go.dev/dl/go1.26.4.src.tar.gz";
      hash = "sha256-T2aKMvv8ETLmqIH7lowvHa2mMUkqM5IRc1+7JVpCYC0=";
    };
  });

  # Re-bind buildGoModule to use our custom Go version
  buildGoModule_1_26_4 = pkgs.buildGoModule.override { go = go_1_26_4; };

  # Script to build and deploy the Go app to the Pi over SSH
  deploy = pkgs.writeShellApplication {
    name = "deploy";
    runtimeInputs = [
      go_1_26_4
      pkgs.openssh
    ];
    text = ''
      host="''${1:-10.136.117.83}"
      user="''${2:-pi}"
      echo "Building for linux/arm64..."
      cd app
      GOOS=linux GOARCH=arm64 go build -o ../build/bedside ./cmd/bedside
      cd ..
      echo "Deploying to ''${user}@''${host}..."
      ssh -o StrictHostKeyChecking=no "''${user}@''${host}" "sudo systemctl stop bedside.service || true"
      scp -o StrictHostKeyChecking=no build/bedside "''${user}@''${host}:/tmp/bedside"
      ssh -o StrictHostKeyChecking=no "''${user}@''${host}" "sudo mv /tmp/bedside /usr/local/bin/bedside && sudo chmod +x /usr/local/bin/bedside && sudo systemctl start bedside.service"
      echo "Deployment complete! Service bedside.service restarted."
    '';
  };

  # Bedside App Go Binary
  bedside-app = buildGoModule_1_26_4 {
    pname = "bedside-app";
    version = "1.0.0";
    src = ../app; # Adjust path relative to this file
    vendorHash = "sha256-jJLJ/WK+YHIcg+N+Jvp6v6RHQxw/XxvXL5MIQbarZns=";
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
          nix build --extra-experimental-features 'nix-command flakes' .#nixosConfigurations.bedside-pi.config.system.build.sdImage
          echo 'Copying image out of container...'
          mkdir -p result-img
          cp -L result/sd-image/*.img* result-img/
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
        echo "Error: You must specify the target disk (e.g., /dev/disk12)"
        echo "Usage: flash-os /dev/disk12"
        echo "Run 'diskutil list' to find your SD card."
        exit 1
      fi
      
      IMG=$(ls result-img/nixos-image-*.img.zst 2>/dev/null | head -n 1 || true)
      if [ -z "$IMG" ]; then
        echo "Error: No compressed image found in result-img/ directory."
        echo "Did you run 'build-os' first?"
        exit 1
      fi

      echo "Unmounting $DISK..."
      diskutil unmountDisk "$DISK" || true

      RDISK="''${DISK/disk/rdisk}"
      echo "Flashing $IMG to $RDISK..."
      echo "This requires sudo privileges."
      zstdcat "$IMG" | sudo dd of="$RDISK" bs=1m
      
      echo "Done! You can now eject the SD card."
    '';
  };

in
{
  inherit
    ffmpeg
    audible-convert
    stage-boot
    deploy
    bedside-app
    build-os
    flash-os
    go_1_26_4
    ;
  default = audible-convert;
}
