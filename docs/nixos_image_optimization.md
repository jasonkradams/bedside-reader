# NixOS Image Optimization

Goal: get the flashed SD image (`result-img/*.img.zst` from `build-os`) down to
**≤150MB compressed**, for this single-purpose bedside audiobook appliance on a
Raspberry Pi Zero 2 W.

This doc is an inventory + action plan. It is grounded in the *actual* closure of
`nixosConfigurations.bedside-reader` (flake pins `nixpkgs-unstable`, mpv 0.41.0),
not on generic NixOS assumptions. Where a number is estimated rather than
measured, it is marked as such — **only the firmware figure below is measured**.

---

## Measured baseline (ground truth)

Taken from the current-system closure on the running Pi (`du`/`nix path-info -Sh`
against the store):

| Item | Size | Status |
| --- | --- | --- |
| `linux-firmware` (redistributable set) | **762MB uncompressed** — *measured* | Still shipped — the dominant lever |
| Kernel + modules (`linux-rpi-6.18.34-stable_20260609`) | ~133MB modules dir | **Already Pi-specific** — see Lever "kernel" below |
| `mpv` default wrapper tree (ffmpeg, yt-dlp→python, X11/mesa, pulse/pipewire) | large, unmeasured | Still shipped, **almost entirely unused** — second-biggest lever |
| `nixos-manual-html` + man/info + `perl` | tens–100+ MB, unmeasured | Docs **not** disabled yet |
| `/run/current-system/sw/bin` | **836 binaries** | `profiles/base.nix` installer utilities (see below) |

What is **already done** in `system/configuration.nix` (do not re-recommend):
Pi-specific `linux-rpi` kernel; `snd_bcm2835` blacklisted; `panel-mipi-dbi` forced
on; `raspberrypiWirelessFirmware` explicitly installed (required for wlan0);
`zramSwap` 150%; `earlyoom`; `supportedFilesystems` forced to `vfat`+`ext4`.

**Reality check:** the 762MB firmware alone is larger than the entire target
image. Removing it (Lever 1) gets most of the way to the goal on its own; the mpv
slim (Lever 2) covers most of the rest. Everything after that is polish.

---

## Levers, ranked by savings

### 1. Drop redistributable firmware, keep only the Broadcom Wi-Fi blob — **SAFE, ~762MB**

`hardware.enableRedistributableFirmware` is set to `true` by `all-hardware.nix`
(its own declared default is `false`) and is the *only* thing pulling the 762MB `linux-firmware`
tree — AMD/Intel/Realtek/Mellanox blobs this board will never use. Force it off
and rely on the Broadcom blob the config already ships explicitly.

In `system/configuration.nix`:

```nix
hardware.enableRedistributableFirmware = lib.mkForce false;

# ALREADY PRESENT and now load-bearing — DO NOT remove:
hardware.firmware = [
  pkgs.raspberrypiWirelessFirmware   # brcmfmac43430b0-sdio.bin + nvram → wlan0
  # ... panel.bin runCommand ...
];
```

Caveats:
- `mkForce` (priority 50) is required to beat the module default (100); a bare
  `= false` will not win.
- Once redistributable firmware is off, `all-firmware.nix` no longer pulls
  `raspberrypiWirelessFirmware` — wlan0 firmware survives **solely** because
  `hardware.firmware` lists it. **Delete that line and wlan0 dies**
  (`brcmfmac43430b0-sdio.bin`, firmware load `-2`).
- The attribute is spelled `raspberrypiWirelessFirmware` (lowercase "pi"). The
  capital-P form `raspberryPiWirelessFirmware` does **not** exist in nixpkgs and
  will fail to evaluate — do not "correct" it.
- Side effect: `hardware.wirelessRegulatoryDatabase` defaults to the same flag, so
  forcing firmware off also drops `wireless-regdb`. Harmless for a 2.4GHz-only Pi
  Zero 2 W; if you want it back, `hardware.wirelessRegulatoryDatabase = true;`
  (it is tiny).
- Does **not** touch the ST7789 panel or SD/DMA — those are kernel modules plus
  the separately-shipped `panel.bin`, not `linux-firmware`.

### 2. Build mpv audio-only — **SAFE, ~200–400MB (estimate)**

The app uses mpv purely as an ALSA audio backend
(`--idle --no-video --really-quiet --no-config --ao=alsa
--audio-device=alsa/plughw:CARD=MAX98357A,DEV=0`; see
`app/internal/player/player.go`). The UI is a **native Go framebuffer renderer**
that mmaps `/dev/fbN` and writes RGB565 to the ST7789 panel (`app/internal/ui/ui.go`)
— there is no browser, no GL, no compositor.

But `pkgs.mpv` (the default wrapper) drags in a large, entirely unused tree: full
`ffmpeg`, the yt-dlp wrapper (→ `python3.13` + `requests`/`websockets`/
`pycryptodomex`/`brotli`/`curl-cffi`/`rich`), the X11 stack + `mesa`/`libgbm`/
`libdrm`, `wayland`, `vulkan-loader`/`shaderc`, `libva`/`libvdpau`,
`libpulseaudio`, and `pipewire`. None of that is reachable in this appliance.

Build the unwrapped player with everything but ALSA turned off, against
`ffmpeg-headless` (the same derivation the service already carries):

```nix
let
  mpvAudio = pkgs.mpv-unwrapped.override {
    ffmpeg             = pkgs.ffmpeg-headless;  # top-level arg; drops full ffmpeg
    alsaSupport        = true;   # KEEP — the one output path the app uses
    x11Support         = false;
    waylandSupport     = false;
    drmSupport         = false;
    vulkanSupport      = false;
    vaapiSupport       = false;
    vdpauSupport       = false;
    pulseSupport       = false;
    pipewireSupport    = false;
    jackaudioSupport   = false;  # exact name (not "jackSupport")
    openalSupport      = false;
    cacaSupport        = false;
    bluraySupport      = false;
    dvdnavSupport      = false;
    dvbinSupport       = false;
    cmsSupport         = false;  # lcms2
    zimgSupport        = false;
    archiveSupport     = false;  # libarchive
    rubberbandSupport  = false;
    bs2bSupport        = false;
  };
in
```

Then replace **both** mpv references:

```nix
environment.systemPackages = [ bedside-app mpvAudio ];
# and in the bedside service:
path = [ mpvAudio pkgs.ffmpeg-headless ];
```

The app runs mpv with `--no-config` and no Lua/yt-dlp, so the unwrapped binary is
functionally complete — you do **not** need the `mpv` wrapper. (If you want the
wrapper anyway: `pkgs.mpv.override { mpv-unwrapped = mpvAudio; youtubeSupport = false; }`.)

Caveats / honesty:
- This does **not** remove Python. `mpv-unwrapped` ships the `umpv` helper script
  and patch-shebangs it, so `python3.13` (~50MB) stays regardless. Dropping yt-dlp
  removes the yt-dlp-specific Python *libraries* (the big tree), not the base
  interpreter. Don't claim "no python."
- `ffmpeg-headless` is the same derivation the `bedside` service already lists, so
  building mpv against it **dedups** — net effect is deleting the full-ffmpeg
  variant, not adding a second ffmpeg.
- `libass`/`libplacebo`/`freetype` are unconditional mpv deps and remain (small).
- The ~200–400MB figure is an estimate; re-measure after building.

### 3. Disable documentation — **SAFE, ~50–150MB (estimate)**

Headless appliance; nothing reads man/info/HTML on-device. All option names below
exist in `nixos/modules/misc/documentation.nix`:

```nix
documentation.enable        = false;  # master switch; gates the rest
documentation.nixos.enable  = false;  # NixOS HTML manual
documentation.man.enable    = false;
documentation.doc.enable    = false;
documentation.info.enable   = false;
```

Removes `nixos-manual-html`, man-db/man-pages, and info pages.
`documentation.enable = false` alone is sufficient; the sub-options are
belt-and-suspenders. Note: this does **not** free `w3m` — that comes from
`base.nix`'s `systemPackages`, so it is dropped by Lever 4, not by the
documentation module.

Note: this does **not** free `perl` — the activation tool is already the Rust
`switch-to-configuration-ng`, so perl's only remaining puller is
`environment.defaultPackages` (Lever 4).

### 4. Strip `profiles/base.nix` installer utilities — **RISKY, tens of MB (estimate)**

This is the real source of the 836 binaries, **not** `defaultPackages`. The SD
image module (`sd-image-aarch64.nix`) imports `profiles/base.nix`, which adds
directly to `environment.systemPackages`: `testdisk`, `ms-sys`, `efibootmgr`,
`efivar`, `parted`, `gptfdisk`, `ddrescue`, `ccrypt`, `cryptsetup`, `vim`,
`fuse`/`fuse3`, `sshfs-fuse`, `socat`, `screen`, `tcpdump`, `sdparm`, `hdparm`,
`smartmontools`, `pciutils`, `usbutils`, `nvme-cli`, `unzip`, `zip`, `jq`,
`w3m-nographics`. `environment.defaultPackages = []` removes **none** of these.

To drop them, add to the flake's module list:

```nix
disabledModules = [ "profiles/base.nix" ];
```

**RISKY — audit first:** `base.nix` also sets `boot.supportedFilesystems` (already
`mkForce`d to `vfat`+`ext4` here, so covered) and `networking.hostId`. Removing
the profile means you own those settings. Verify the image still builds and boots
before keeping this. Bigger win than Lever 5 but not risk-free.

### 5. Empty `environment.defaultPackages` — **SAFE, low impact**

```nix
environment.defaultPackages = lib.mkForce [ ];
```

Honesty: the default is only `[ perl rsync strace ]`. Emptying it drops just
those three (`perl` ~50MB is the only sizeable one, and it does drop because
`switch-to-configuration-ng` is the activation tool). This does **not** explain
the 836 binaries — that's Lever 4. `corePackages` (coreutils, util-linux, curl,
gnugrep, procps, …) are retained regardless, so the bin count stays high. Dropping
`rsync` is fine — colmena/`nixos-rebuild` copy closures via `nix-copy`/SSH.

### 6. Strip the Go binary — **SAFE, ~5–15MB**

In `nix/packages.nix`, on the `bedside-app` `buildGoModule_1_26_4` call:

```nix
bedside-app = buildGoModule_1_26_4 {
  pname = "bedside-app";
  version = "1.0.0";
  src = ../app;
  vendorHash = "sha256-jJLJ/WK+YHIcg+N+Jvp6v6RHQxw/XxvXL5MIQbarZns=";
  ldflags = [ "-s" "-w" ];   # strip DWARF + symbol table
};
```

`buildGoModule` honors `ldflags` natively. Marginal next to Levers 1–2; trades
away useful panic traces / `delve`, acceptable for a release appliance.

### Kernel & initrd modules — already Pi-specific; module stripping is the only, RISKY, remainder

The system is **already** on `linux-rpi` (`linux-rpi-6.18.34-stable_20260609`,
~133MB modules) — do **not** treat "switch to the rpi kernel" as an open action;
it is done. The only remaining kernel-side lever is stripping unused modules, and
it is **RISKY** here: the config deliberately adds `sdhci_bcm2835`, `sdhci_iproc`,
`bcm2835_dma`, `i2c_bcm2835` to stage-1 to mount the SD root, so
`boot.initrd.includeDefaultModules = false` (or aggressive module pruning) can
regress boot. Not recommended unless you re-verify the SD host controller and DMA
drivers still load. Likely low payoff versus Levers 1–2.

### Non-levers (do not spend budget here)

- **`services.udisks2.enable = false` — NO-OP.** Its default is already `false`
  and nothing in this config, the SD-image module, or `nixos-hardware`
  raspberry-pi-3 enables it. Harmless to set explicitly; saves 0.
- **`environment.noXlibs` — REMOVED, do not use.** The option was removed in NixOS
  24.11 (`mkRemovedOptionModule` in `rename.nix`); setting it is a hard evaluation
  error. There is **no** drop-in successor option — nixpkgs' guidance is "apply
  similar overlays yourself." Prefer Lever 2, which removes the X11/GL/mesa tree at
  its actual source (mpv) without a global-overlay blast radius.
- **PipeWire/PulseAudio system services — nothing to disable.** No
  `services.pipewire`/`hardware.pulseaudio` is enabled; pulse/pipewire enter the
  closure *through the mpv wrapper*, so Lever 2 is the fix, not a system audio
  toggle.

---

## Summary — measured results and the real floor

**Measured** with `build-os` (compressed `result-img/*.img.zst`) and
`nix path-info` on the `sdImage` toplevel closure (nixpkgs pin `26.11pre` /
`89570f2`; every non-firmware estimate above is now superseded by these numbers):

| Build | Compressed image | Closure (uncompressed) |
| --- | --- | --- |
| Baseline — committed app fixes, **no size levers** | **1563 MB** | 1473 MB |
| + firmware off · audio-only mpv · docs off · `defaultPackages=[]` · Go `-s -w` | **602 MB** | ~1140 MB |
| + drop embedded nixpkgs source · drop `base.nix` tools · `disableInstallerTools` · drop mpv `umpv` (evicts python3) | **466 MB** | **1043 MB** |
| same closure recompressed at `zstd -19` (free, see below) | **392 MB** | — |

That is a **70% cut** from the 1.5GB baseline, all from safe, reproducible config
(`system/image-optimization.nix` + the `mpvAudio` derivation and `-s -w` in
`system/configuration.nix` / `nix/packages.nix`). It does **not** reach 150MB, and
the closure analysis shows why.

### The floor: ≤150MB is not reachable for a functional appliance

After the levers above, the 1043MB closure is almost entirely **irreducible
infrastructure** for a functional systemd-NixOS + audio appliance:

| Item | Uncompressed | Why it stays |
| --- | --- | --- |
| linux-rpi kernel + modules + initrd | ~189 MB | the Pi kernel; stripping stage-2 modules needs a custom kernel build (risky), and removing *all* modules still only reaches ~350MB compressed |
| systemd (+ systemd-minimal) | ~101 MB | the app is a `Type=notify` watchdog service; the whole boot/service model is systemd |
| perl | ~57 MB | NixOS `update-users-groups.pl` activation (user/group management) |
| nix + boost + icu4c | ~92 MB | `nix` is required **on the device** for `deploy-os` / colmena `nix-copy` |
| glibc | ~44 MB | libc |
| util-linux + coreutils + bash | ~75 MB | base userland (mount, login, activation) |
| mpv (slim) + ffmpeg-headless | ~55 MB | the audio player + `ffprobe` metadata |

At the default zstd level these compress to ~200–250MB; even at `zstd -22` the
floor is ~200MB. Reaching 150MB would require removing systemd (breaks the app),
removing `nix` (breaks remote deploy), or swapping glibc/coreutils for
musl/busybox plus a custom minimal kernel — each sacrifices the "functional app"
(or `deploy-os`) constraint. **Realistic target: ~250MB.**

### Free / marginal levers (diminishing returns, not applied)

- **Higher zstd level (~74MB)** — the closure recompresses 466→392MB at `zstd -19`
  (and a bit lower at `-22`). The `sd-image` module hardcodes `zstd --rm` at the
  default level with no level option, so this needs an overlay on the image
  derivation or a post-build `zstd -d … | zstd -19` step.
- **Audio-only ffmpeg (~25MB)** — override `ffmpeg-headless` with
  `withX265=false; withDav1d=false; withAom=false; withVpx=false; withSvtav1=false`
  (keep AAC/mp3 decode + mov/mp4 demux for playback + `ffprobe`). Drops x265,
  libdovi, dav1d, libaom for a ~15-min ffmpeg rebuild.
- **Drop mpv `libplacebo`/`shaderc`/`glslang` (~30MB)** — mpv pulls the GPU shader
  stack even audio-only; no override flag exists, so it needs a patched mpv. Low
  payoff.

### Verify functionality after cutting

Most levers are pure content cuts, but a few change runtime behavior — deploy the
optimized config (`deploy-os`) and confirm on-device: Wi-Fi still associates (the
Broadcom blob is kept; only unrelated firmware is dropped), audio still
decodes/plays (slim mpv + `ffmpeg-headless`), and the app still renders to the
panel. `base.nix` removal affects **only** the flashed image (colmena never
imports it), so also confirm a fresh `flash-os` image boots and expands its root.

> Related: `docs/bedside-audiobook-appliance.md` documents the full appliance
> architecture — it is realigned to the same native-framebuffer renderer described
> here (no browser engine).
