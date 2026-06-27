{
  description = "Decrypt Audible .aax audiobooks into DRM-free .m4b with a minimal, source-built ffmpeg";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems =
        f: nixpkgs.lib.genAttrs systems (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (
        system: pkgs:
        let
          # Custom ffmpeg derivation, built from a pinned source tarball.
          #
          # Stripped down to exactly what an Audible .aax -> .m4b stream copy
          # needs: the aax/mov demuxers, the ipod/mp4 muxers, the file
          # protocol, and aac/mjpeg passthrough. --disable-asm avoids the
          # gas-preprocessor toolchain on darwin and costs nothing here because
          # we never re-encode.
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
              # input: Audible .aax and the mov/mp4 family it is based on
              "--enable-demuxer=aax,mov"
              "--enable-protocol=file"
              "--enable-parser=aac,mjpeg"
              "--enable-decoder=aac,mjpeg"
              # output: .m4b (ipod muxer) plus the mov/mp4 family
              "--enable-muxer=ipod,mp4,mov"
              "--enable-bsf=aac_adtstoasc"
            ];

            # ffmpeg ships a hand-written configure that rejects the generic
            # autoconf flags stdenv would inject, so drive configure directly.
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
              platforms = systems;
              mainProgram = "ffmpeg";
            };
          });

          # Wraps the conversion loop and pins it to the ffmpeg above.
          # Activation bytes stay out of the flake (they are account-specific):
          # pass them via $AUDIBLE_ACTIVATION_BYTES.
          audible-convert = pkgs.writeShellApplication {
            name = "audible-convert";
            runtimeInputs = [ ffmpeg ];
            text = ''
              # Decrypt every *.aax in a directory into *.m4b (lossless stream copy).
              #
              #   AUDIBLE_ACTIVATION_BYTES=xxxxxxxx audible-convert [DIR]
              #
              # DIR defaults to the current directory. Get the bytes with:
              #   audible activation-bytes

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
        in
        {
          inherit ffmpeg audible-convert;
          default = audible-convert;
        }
      );

      apps = forAllSystems (
        system: _pkgs: {
          default = {
            type = "app";
            program = "${self.packages.${system}.audible-convert}/bin/audible-convert";
          };
        }
      );

      devShells = forAllSystems (
        system: pkgs: {
          default = pkgs.mkShell {
            packages = [ self.packages.${system}.ffmpeg ];
          };
        }
      );
    };
}
