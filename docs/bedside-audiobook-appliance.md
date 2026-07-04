# Bedside Audiobook Appliance

**Architecture, BOM, and Build Plan**
_Go (native framebuffer) + NixOS + Pi Zero 2 W + Display HAT Mini + MAX98357A + CE32A-4 (mono)_

---

## 0. Decisions locked

**This is the Tier 2 lean build.** Earlier revisions of this doc went chasing premium components — Pi 4, 5" Touch Display, stereo speakers, DS3231 RTC, MiniAmp DAC HAT — most of which weren't earning their cost for a bedside spoken-word player. This rev strips back to the minimum that delivers the experience: standalone bedside operation, a screen for browsing + now-playing (a clock display is planned, not yet built), audio that fills a quiet bedroom.

| Axis         | Choice                                                                     | Why it matters downstream                                                                                                                                                                 |
| ------------ | -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| SBC          | **Raspberry Pi Zero 2 W**                                                  | $15 board, fanless idle, fits the smallest enclosure. A single Go binary drawing straight to the panel framebuffer sips RAM; there is no browser to fit into 512MB.                        |
| Display      | **Pimoroni Display HAT Mini** (2.0" IPS SPI + 4 onboard buttons + RGB LED) | Stacks directly on the Pi header. Replaces the separate display + buttons + bezel work. Software-controllable backlight to true zero.                                                     |
| Audio        | **Adafruit MAX98357A + 1× Dayton CE32A-4** (mono)                          | $6 I2S amp breakout instead of a $32 HAT. Single 1.25" 4Ω driver. Mono for spoken word is fine — at this driver size and bedside distance you wouldn't perceive much stereo image anyway. |
| Clock source | NTP only (no DS3231 RTC)                                                   | Saves $20 + I2C wiring. Accept that the clock reads briefly wrong after a power-loss-plus-wifi-down event. Easy to add later as a $3 breakout.                                            |
| Sync         | Standalone / Offline-first                                                 | Completely decoupled from Audiobookshelf. Audiobooks currently reach local storage by manual copy (e.g. `scp`/`rsync` over SSH); a Samba (SMB) share for drag-and-drop transfers is planned, not configured today.                       |
| **Renderer** | **Native Go → SPI panel framebuffer** — no browser, no Xorg, no HTTP       | The Go binary `mmap`s `/dev/fbN` and draws the 320×240 RGB565 panel directly with `image/draw` + `basicfont`. Tens of MB resident, boots in seconds.                                      |
| **OS**       | **NixOS (declarative SD image)**                                            | Whole device — kernel, DT overlays, the `bedside` service, Wi-Fi — is one reproducible config. Build a signed SD image, then push incremental changes over SSH with colmena.              |

**Estimated total delivered cost: ~$135** (~$77 unique-to-this-build parts).

---

## 1. Architecture

Single Go binary on the Pi. It owns audio playback, GPIO input, backlight, the library scanner, position/system state, and — crucially — it draws the screen itself. There is no web stack: no HTTP server, no HTML, no browser. The binary `mmap`s the SPI panel's framebuffer and paints pixels into it with the Go standard library's `image`/`image/draw` and `golang.org/x/image/font/basicfont`. Everything inside the process is coordinated through an in-process, typed event bus built on channels.

**Renderer:** the `ui` package discovers the panel's `/dev/fbN` by sysfs metadata, `mmap`s it, and renders a `320×240` RGBA canvas that it packs into 16-bit RGB565 and copies into the mmap on every state change. The panel is an ST7789 driven by the kernel `panel-mipi-dbi` driver; the app talks to its DRM framebuffer node, not to any windowing system. There is no Xorg, no Wayland, no compositor — the render loop is a goroutine reacting to bus events plus a Wi-Fi status poller. `main.go` logs this on startup: "Starting Bedside Audiobook Appliance (Native Framebuffer Mode)".

### 1.1 Component map

Components within the Go binary, all coordinated through an in-process event bus (channels).

| Component       | Responsibility                                                                                               | Inputs                  | Outputs                            |
| --------------- | ------------------------------------------------------------------------------------------------------------ | ----------------------- | ---------------------------------- |
| **Player**      | mpv JSON-IPC client over a unix socket; play/pause/seek/volume/skip-chapter; emits progress ticks            | Commands from event bus | PlaybackState events               |
| **Library**     | Local filesystem scanner using `ffprobe` to extract metadata and chapters into bbolt                         | Local audio files       | bbolt catalog updates              |
| **Input**       | GPIO polling: rotary encoder (quadrature decode), HAT buttons (debounced); publishes input events            | periph.io GPIO          | InputEvent (rotate, click, button) |
| **Display**     | Backlight on/off via `/sys/class/backlight` if present, else BCM GPIO13 sysfs; wake/blank on input + timer   | InputEvent, screen timer| Backlight state                    |
| **Renderer/UI** | `mmap`s the panel framebuffer; draws player + library-menu screens with `image/draw` + `basicfont`; RGB565   | All bus events          | Pixels in `/dev/fbN` (the panel)   |

There is no in-binary "media loader" component today: audiobooks reach `/var/lib/bedside/audiobooks/` by manual copy (e.g. `scp`/`rsync` over SSH). A Samba (SMB) share for drag-and-drop transfers from any OS is a planned convenience, not implemented.

### 1.2 Event flow (state → event bus → framebuffer)

State flows one direction: hardware/timers → event bus → the renderer repaints the framebuffer. Input flows the other way: GPIO watchers publish events onto the same bus, and the App controller turns them into player/menu commands. Nothing leaves the process — there is no socket to the UI, because the UI *is* a goroutine in the same binary.

```
            ┌──────────────┐      ┌─────────────────┐
GPIO ----> │  Input       │─┐    │  Local Storage  │
            │  (periph.io) │ │    │ (ffprobe scan)  │
            └──────────────┘ │    └────────^─────────┘
                             │             │ Filesystem
                             v             │
                       ┌─────────────────────────┐
                       │     Event Bus (chans)    │
                       │   pubsub of typed events │
                       └────────┬───────┬────────┘
                                │       │
                ┌───────────────┘       └────────────────┐
                v                                        v
        ┌──────────────┐                          ┌──────────────┐
        │   Player     │── mpv JSON IPC --> mpv ->│  MAX98357A   │
        │              │   (unix socket)          │  I2S breakout│
        └──────────────┘                          └──────┬───────┘
                                                         │ I2S
                                                         v
        ┌──────────────┐   copyToRGB565           ┌──────────────┐
        │  Renderer/UI │── (mmap write) --------->│  ST7789 panel│
        │  image/draw  │                          │  /dev/fbN    │
        │  basicfont   │                          │  (SPI, 320×  │
        │              │                          │   240 RGB565)│
        └──────────────┘                          └──────────────┘
```

### 1.3 Why native framebuffer (no browser)

- **Nothing to fit into 512MB.** The whole reason earlier revisions agonized over renderer choice was that a browser is the heaviest thing on the device. Delete the browser and the problem evaporates: a Go binary that `mmap`s the framebuffer costs tens of MB, not hundreds.
- **The screen is 320×240.** At this size there is no layout engine worth carrying. `image/draw` fills rectangles and `basicfont.Face7x13` stamps text; that is the entire UI toolkit we need.
- **One process, one language, one failure domain.** No HTTP server to bind, no event stream to reconnect, no localhost round-trip, no `npm`/bundler/source-maps to debug at 2am. State lives in Go structs; the render function reads them and paints.
- **Boot is trivial.** There is no second process to sequence after the Go binary — no browser to launch, no `network-online` gate for a local socket. `systemd` starts one unit and the screen lights up.

### 1.4 Render pipeline

The renderer holds an `image.RGBA` canvas the size of the panel. On each repaint it clears the background, draws the player screen, optionally overlays the library menu, stamps the Wi-Fi icon, then calls `copyToRGB565` to pack the RGBA canvas into the 16-bit little-endian RGB565 layout the panel expects and writes it straight into the `mmap`. Repaints are driven by two goroutines: a bus subscriber that repaints on `PlayerStateChanged` / `PlayerProgressTick` / `MenuUpdate`, and a Wi-Fi poller that reads `/sys/class/net/wlan0/carrier` every 2s and repaints on change. See §6 for the screen layouts and a faithful code sketch.

### 1.5 Why Pi Zero 2 W is enough

With no browser, the Zero 2 W's 512MB RAM stops being the binding constraint. The resident set is the Go binary plus the mpv subprocess — comfortably tens of MB, not hundreds — leaving the rest of RAM free for the page cache and zram swap. CPU is light: spoken-word decode is near-idle, and the renderer only repaints on state changes (a full `320×240` RGB565 frame is 150KB, packed and copied in well under a frame's worth of time). There is no runtime cover-art decode to worry about because we do not render cover art on the panel at all.

**Power**: a Zero 2 W under this load draws roughly 500–800mA at 5V — comfortable on a 2.5A micro-USB PSU with the MAX98357A on the same supply. No PSU oversizing needed.

**Thermal**: idle 45–55°C in a closed plastic case at 22°C ambient, no throttling. The Pi 4 heatsink case we previously specced was overkill — we don't need it.

---

## 2. Go library recommendations

### 2.1 Audio playback

**Use mpv via JSON IPC, not a Go-native decoder.** This is the controversial pick so I'll defend it. `beep` and `oto` decode in Go and are great for games and effects, but they don't handle: gapless chapter transitions across M4B parts, M4B chapter metadata, accurate seek across VBR MP3, ReplayGain, or buffered HTTP streaming. Reimplementing those is a months-long detour.

mpv handles all of it, runs as a child process, and exposes a stable JSON-over-unix-socket protocol. You connect, send `{"command":["set_property","pause",false],"request_id":N}`, observe `time-pos` / `duration` / `volume`, skip chapters with `add chapter ±1`, and you're done. The socket lives at `/var/lib/bedside/mpv.sock`; the player dials it with a short retry loop because mpv re-creates it on start.

| Option                                                    | Verdict        | Notes                                                                                                                                                             |
| --------------------------------------------------------- | -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **mpv + JSON IPC** (Recommended)                          | Use this       | Spawn `mpv --idle --no-video --really-quiet --no-config --ao=alsa --audio-device=alsa/plughw:CARD=MAX98357A,DEV=0 --input-ipc-server=/var/lib/bedside/mpv.sock`. Tiny Go client. |
| **libmpv via cgo** (`gen2brain/go-mpv` or write your own) | Acceptable     | Tighter integration, no subprocess. Adds cgo build complexity for marginal gain on a Pi.                          |
| **beep / oto**                                            | Skip for this  | No M4B chapter handling, no gapless, you'd be writing a player not an appliance.                                  |
| **MPRIS over D-Bus** (`godbus/dbus`)                      | Optional layer | Nice if you want phone-as-remote later. Add as a thin adapter on top of the Player; not the primary control path. |

### 2.2 GPIO

| Library                                                     | Verdict    | Notes                                                                                                                |
| ----------------------------------------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------------- |
| **periph.io/x/conn/v3 + periph.io/x/host/v3** (Recommended) | Use this   | Actively maintained, idiomatic Go, clean pin API via `gpioreg`. This build polls: the encoder A/B pins every 2ms for quadrature, buttons every 5ms with a 50ms debounce. All pins are `PullUp, NoEdge`. |
| `stianeikeland/go-rpio`                                     | Avoid      | mmaps `/dev/mem` — no reason to reach that low for a few buttons and one encoder.                                    |
| `warthog618/go-gpiocdev`                                    | Acceptable | Modern character-device API. Lower-level than periph.io. Use directly if you want zero abstraction.                  |

### 2.3 Local Library Scanner

Instead of an API client, the appliance scans the local directory (`/var/lib/bedside/audiobooks/`) directly. We rely on **`ffprobe`** (supplied by `ffmpeg-headless` on the service path) to handle metadata extraction because it handles M4B chapters correctly, avoiding the need for complex Go tagging libraries.

A Go goroutine runs on-demand and again on a 5-minute timer:
1. Walks the (flat) directory — subdirectories are skipped — looking for `.m4b`, `.mp3`, `.m4a`.
2. Computes a stable ID = first 12 bytes of the SHA-256 of the base filename, hex-encoded; skips the file if that ID is already in the DB.
3. Runs `ffprobe -v quiet -print_format json -show_format -show_chapters -show_streams /path/to/file` and unmarshals the JSON (duration, title, artist, and per-chapter id/title/start/end).
4. Writes the book as JSON into the bbolt `library` bucket.

Cover art is reserved but not yet wired: the cover directory is created and `Audiobook.CoverHash` exists as a field, but `probeFile` does not populate it and the panel UI does not render cover images (see §6). Extraction is a future step, not a current one.

### 2.4 Rendering, GPIO, and storage libraries

There is no web-framework layer to pick — the UI is standard-library drawing straight to the framebuffer. What's left is a small, boring dependency set:

| Layer                 | Library                                                               | Notes                                                                                    |
| --------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Framebuffer drawing   | stdlib `image`, `image/color`, `image/draw`                           | RGBA canvas; `draw.Draw` fills the background and rectangles (progress bar, menu rows).   |
| Text                  | [golang.org/x/image/font/basicfont](https://pkg.go.dev/golang.org/x/image/font/basicfont) | `basicfont.Face7x13` stamped via a `font.Drawer` (with `x/image/math/fixed`).            |
| Framebuffer mmap      | [golang.org/x/sys/unix](https://pkg.go.dev/golang.org/x/sys/unix)     | `unix.Mmap` of `/dev/fbN`; `FBIOBLANK` ioctl (`0x4611`) to unblank.                       |
| Audio                 | [mpv (JSON IPC)](https://mpv.io/manual/stable/#json-ipc)              | Spawn as a subprocess; control via unix socket.                                          |
| GPIO                  | [periph.io/x/conn/v3](https://periph.io/) + `x/host/v3`               | Idiomatic Go; the app polls the pins (no edge callbacks).                                 |
| Metadata extraction   | `os/exec` wrapping `ffprobe`                                          | Extract chapters and tags directly from media files.                                     |
| Storage               | [go.etcd.io/bbolt](https://github.com/etcd-io/bbolt)                  | Single-file KV; atomic writes survive yanked power.                                       |
| systemd integration   | [coreos/go-systemd/v22 daemon](https://github.com/coreos/go-systemd)  | `sd_notify(READY=1)` and watchdog pings for the `Type=notify` unit.                       |
| Logging               | [stdlib log](https://pkg.go.dev/log)                                 | Lines to journald.                                                                        |

### 2.5 Other useful bits

- **bbolt buckets**: three of them — `library` (book catalog, JSON per book), `progress` (position stored as a `%f` string keyed by base filename), and `system` (a single `SystemState` JSON under `system_state`, defaulting timeout=5, volume=50, encoderMode="vol").
- **Position persistence**: written on progress ticks but **throttled to at most once every 10s** so the SD card isn't hammered; position is reset to 0 only on a true `eof` end-of-file. Survives a power yank because bbolt fsyncs.
- **Search**: if/when needed, a simple `strings.Contains` over normalized titles walking the `library` bucket — no index required at this catalog size.

---

## 3. Bill of materials

**Single lean build.** No more A/B testing across two SBCs — earlier revisions kept a Pi 4 + Touch Display 2 alongside a Zero 2 W + Display HAT Mini for comparison. Once the renderer became a native framebuffer draw (no browser) there was no reason to spend Pi 4 money, so we're committing to the smaller, cheaper path. Total delivered cost lands around **$135**, with about **$77** of that being unique-to-this-build parts (the rest are PSU/SD/jumpers/case that you might already own).

### 3.1 Compute

| Item    | Part                                                                                                                   | Supplier  | Qty | Delivered | Notes                                                                                                                                    |
| ------- | ---------------------------------------------------------------------------------------------------------------------- | --------- | --- | --------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| SBC     | [Raspberry Pi Zero 2 W (with headers pre-soldered)](https://www.pishop.us/product/raspberry-pi-zero-2-wh/)             | PiShop.us | 1   | ~$24      | Pre-soldered headers ($3 premium, worth it). Headerless is $15 + soldering 40 pins.                                                      |
| microSD | [SanDisk High Endurance 32GB](https://www.amazon.com/SanDisk-Endurance-microSDXC-Adapter-Monitoring/dp/B07P3D6Y5B)     | Amazon    | 1   | ~$10      | Endurance class matters because we'll write position state ~once/sec. 32GB is plenty — the NixOS root (Nix store + Go binary) is comfortably <2GB, leaving the rest for the audiobook library. |
| PSU     | [CanaKit 2.5A micro-USB PSU (UL listed)](https://www.amazon.com/CanaKit-Raspberry-Supply-Adapter-Listed/dp/B00MARDJZ4) | Amazon    | 1   | ~$10      | Zero 2 W uses micro-USB power, not USB-C. 2.5A absorbs MAX98357A current transients comfortably.                                         |
| Case    | [Basic Pi Zero 2 W plastic case](https://www.amazon.com/s?k=raspberry+pi+zero+2+w+case)                                | Amazon    | 1   | ~$6       | Generic two-piece snap case. We're not heatsinking — idle thermals are fine in a closed plastic shell.                                   |

### 3.2 Display

| Item                    | Part                                                                                      | Supplier  | Qty | Delivered | Notes                                                                                                                                                                       |
| ----------------------- | ----------------------------------------------------------------------------------------- | --------- | --- | --------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Display + buttons + LED | [**Pimoroni Display HAT Mini (PIM589)**](https://www.pishop.us/product/display-hat-mini/) | PiShop.us | 1   | ~$30      | 2.0" 320×240 IPS SPI. **Four onboard tactile buttons** (A/B/X/Y on GPIO 5/6/16/24) + RGB LED — replaces a separate button board entirely. Stacks directly on the Pi header. |

### 3.3 Audio

| Item             | Part                                                                                                                                   | Supplier      | Qty | Delivered | Notes                                                                                                                                            |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------- | ------------- | --- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| I2S amp breakout | [**Adafruit MAX98357A** I2S Class-D Mono Amp](https://www.adafruit.com/product/3006)                                                   | Adafruit      | 1   | ~$13      | $6 + ~$7 USPS. 3W mono at 4Ω from a tiny breakout. Talks I2S to GPIO 18/19/21. Driven by the `dtoverlay=max98357a,sdmode-pin=26` overlay (kernel `snd-soc-max98357a` + `simple-audio-card`). |
| Speaker driver   | [**Dayton Audio CE32A-4** 1.25" Mini Speaker, 4Ω](https://www.parts-express.com/Dayton-Audio-CE32A-4-1-1-4-Mini-Speaker-4-Ohm-285-103) | Parts Express | 1   | ~$15      | Single driver, mono. Laptop-scale full-range. $5 driver + $9.95 PE flat ground. Living with mono first; add a second later if you miss stereo.   |

### 3.4 Controls

| Item                  | Part                                                                                                            | Supplier        | Qty | Delivered | Notes                                                                                                                                                                           |
| --------------------- | --------------------------------------------------------------------------------------------------------------- | --------------- | --- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Rotary encoder + push | [Generic EC11 24-detent encoder w/ pushbutton](https://www.amazon.com/s?k=ec11+rotary+encoder+with+push+button) | Amazon (5-pack) | 1   | ~$8 for 5 | Generic EC11 from a multipack is fine for a one-off build; same form factor and same connections as the Bourns PEC11R.                                                          |
| Encoder knob          | [Aluminum 20mm D-shaft or round-shaft knob](https://www.amazon.com/s?k=aluminum+knob+20mm+6mm)                  | Amazon          | 1   | ~$5       | Any aluminum knob that fits a 6mm shaft. Adafruit #5527 ($3 + Adafruit shipping = ~$13) is the pretty option; a 4-pack on Amazon is $5 if you can live with anonymous aluminum. |

**HAT buttons replace the discrete button bank.** Map them in software: A=Play/Pause, B=Menu, X=Previous chapter, Y=Next chapter. The rotary encoder handles scrolling the library list and adjusting volume.

### 3.5 Wiring & enclosure

| Item                  | Part                                                                                          | Supplier                  | Qty | Delivered | Notes                                                                                                        |
| --------------------- | --------------------------------------------------------------------------------------------- | ------------------------- | --- | --------- | ------------------------------------------------------------------------------------------------------------ |
| Hookup wire + jumpers | [Dupont jumper kit, M-F/M-M/F-F (120 pcs)](https://www.amazon.com/s?k=dupont+jumper+wire+kit) | Amazon                    | 1   | ~$7       | For an internal-only, low-current build, Dupont jumpers are fine and need no soldering.                      |
| Enclosure             | PETG printed (your design) or repurposed box                                                  | Self-print or junk drawer | 1   | ~$0–10    | Drop the threaded inserts and standoff kit until you've actually printed a prototype and know what you need. |

### 3.6 Cost summary

| Line                                     | Cost      | Notes                                                                |
| ---------------------------------------- | --------- | -------------------------------------------------------------------- |
| Compute (Pi Zero 2 WH + SD + PSU + case) | ~$50      | Pre-headered Pi is worth the $3 premium.                             |
| Display (Display HAT Mini)               | ~$30      | Includes 4 buttons + RGB LED.                                        |
| Audio (MAX98357A + CE32A-4 speaker)      | ~$28      | Mono, single driver.                                                 |
| Controls (encoder + knob)                | ~$13      | HAT buttons cover everything else.                                   |
| Wiring + enclosure                       | ~$15      | Dupont jumpers + scrap or printed case.                              |
| **Total delivered (estimated)**          | **~$135** | Includes shipping across PiShop / Amazon / Parts Express / Adafruit. |

**Honest pricing read**: the $70 floor I quoted earlier assumed everything lived in one Amazon Prime order. Real-world the build lands closer to $135 because the Pi (via PiShop) and the MAX98357A (via Adafruit) carry per-order shipping. If you already have a PSU, SD card, jumper wires, or a case in a drawer, the marginal cost drops fast — the unique-to-this-build parts are roughly **$77**: Pi Zero 2 WH (~$24) + Display HAT Mini (~$30) + MAX98357A (~$13) + CE32A-4 (~$15).

**Order plan** (3 orders, not 6):

- **PiShop.us**: Pi Zero 2 WH + Display HAT Mini. ~$54 + $7 USPS = ~$61.
- **Adafruit**: MAX98357A. ~$6 + $7 USPS = ~$13.
- **Amazon Prime**: SD card, PSU, case, encoder, knob, Dupont jumpers. ~$36 delivered, no shipping.
- **Parts Express**: 1× CE32A-4 ($5) + $9.95 flat ground = ~$15. Annoying minimum-order; if you can scrounge a speaker from a junk drawer at least for prototyping, skip this entirely until you're sure.

---

## 4. Wiring & GPIO

### 4.1 Pin map (BCM numbering)

Two HATs cohabit cleanly: the Display HAT Mini (SPI + button pins) and the MAX98357A (I2S pins). No conflicts — the breakout sits to one side of the stacking header and the HAT goes on top.

| BCM               | Phys | Used by                                     | Notes                                                                    |
| ----------------- | ---- | ------------------------------------------- | ------------------------------------------------------------------------ |
| GPIO4             | 7    | **Rotary encoder A**                        | Free GPIO; polled (no edge interrupts).                                  |
| GPIO5             | 29   | **Display HAT Mini: Button A**              | Map to Play/Pause.                                                       |
| GPIO6             | 31   | **Display HAT Mini: Button B**              | Map to Menu.                                                             |
| GPIO7 (CE1)       | 26   | **Display HAT Mini: SPI CE1**               | ST7789 chip-select (`dtoverlay=mipi-dbi-spi,spi0-1`).                    |
| GPIO9 (MISO)      | 21   | **Display HAT Mini: DC**                    | Repurposed as data/command.                                              |
| GPIO10 (MOSI)     | 19   | **Display HAT Mini: SPI MOSI**              |                                                                          |
| GPIO11 (SCLK)     | 23   | **Display HAT Mini: SPI clock**             |                                                                          |
| GPIO13            | 33   | **Display HAT Mini: backlight enable**      | On/off via sysfs (no PWM); see §4.4.                                     |
| GPIO16            | 36   | **Display HAT Mini: Button X**              | Map to previous chapter.                                                 |
| GPIO17            | 11   | **Display HAT Mini: RGB LED**               | Multi-channel LED via PWM on shared pins.                                |
| GPIO18 (PCM_CLK)  | 12   | **MAX98357A: BCLK**                         | I2S bit clock.                                                           |
| GPIO19 (PCM_FS)   | 35   | **MAX98357A: LRC**                          | I2S word select.                                                         |
| GPIO20            | 38   | **Rotary encoder B**                        | Free GPIO.                                                               |
| GPIO21 (PCM_DOUT) | 40   | **MAX98357A: DIN**                          | I2S data.                                                                |
| GPIO22            | 15   | **Display HAT Mini: RGB LED**               | Multi-channel LED via PWM on shared pins.                                |
| GPIO23            | 16   | **Rotary encoder push**                     | Free GPIO.                                                               |
| GPIO24            | 18   | **Display HAT Mini: Button Y**              | Map to next chapter.                                                     |
| GPIO25            | 22   | **Display HAT Mini: Reset**                 | ST7789 reset line.                                                       |
| GND               | 25   | **Display HAT Mini: SPI Ground**            | **CRITICAL** for 60MHz SPI return path; must connect.                    |
| GPIO26            | 37   | **MAX98357A: SD / SD_MODE**                 | Hardware Mute (Low = Mute, High = Awake). Eliminates I2S pops.           |
| GPIO27            | 13   | **Display HAT Mini: RGB LED (one channel)** | Multi-channel LED via PWM on shared pins; details in Pimoroni schematic. |

### 4.2 Wiring diagram

Because the **Display HAT Mini** plugs directly into the entire 40-pin header, the external components (Audio Amp and Rotary Encoder) must be wired by either soldering to the underside of the Pi's GPIO pins, or by using an extra-tall "stacking header" that lets the pins protrude through the Display HAT.

Here is the exact mapping of the 40-pin header to the external components:

```text
                     Raspberry Pi 40-Pin Header
                             (Top View)
                           [USB / HDMI side]
      
                +3.3V  [ 1]  [ 2]  5V ----------> MAX98357A (Vin)
         SDA (GPIO 2)  [ 3]  [ 4]  5V 
         SCL (GPIO 3)  [ 5]  [ 6]  GND ---------> MAX98357A (GND)
Encoder A     (GPIO 4)  [ 7]  [ 8]  TXD
                   GND  [ 9]  [10]  RXD
[HAT LED]    (GPIO 17)  [11]  [12]  (GPIO 18) ---> MAX98357A (BCLK)
               (GPIO 27)[13]  [14]  GND ---------> Encoder (GND / Common)
[HAT LED]    (GPIO 22)  [15]  [16]  (GPIO 23) ---> Encoder (SW+ / Push)
                 3.3V  [17]  [18]  (GPIO 24) [HAT Button Y]
       [HAT SPI MOSI]  [19]  [20]  GND ---------> Encoder (SW- / Push GND)
       [HAT SPI DC]    [21]  [22]  (GPIO 25) [HAT TE]
       [HAT SPI SCLK]  [23]  [24]  (GPIO 8)  
                  GND  [25]  [26]  (GPIO 7)  [HAT SPI CS / CE1]
                ID_SD  [27]  [28]  ID_SC
[HAT Button A]         [29]  [30]  GND
[HAT Button B]         [31]  [32]  (GPIO 12)
[HAT Backlight]        [33]  [34]  GND
MAX98357A (LRC) <---   [35]  [36]  (GPIO 16) [HAT Button X]
    MAX98357A (SD) <-- [37]  [38]  (GPIO 20) ---> Encoder B
                  GND  [39]  [40]  (GPIO 21) ---> MAX98357A (DIN)

                           [micro-SD side]
```

MAX98357A breakout wiring:
  LRC    --> GPIO19 (PCM_FS,   physical pin 35)
  BCLK   --> GPIO18 (PCM_CLK,  physical pin 12)
  DIN    --> GPIO21 (PCM_DOUT, physical pin 40)
  GAIN   --> leave floating (default 9dB)
  SD     --> GPIO26 (BCM, phys 37). Used for hardware mute (Low = mute, High = wake).
  GND    --> Pi GND (header pin 6, 9, 14, ...)
  Vin    --> Pi 5V  (header pin 2 or 4)
  + / -  --> CE32A-4 speaker terminals

Rotary encoder (generic EC11):
  3-Pin Side: Left (A)    --> GPIO4 (BCM, phys 7)
  3-Pin Side: Right (B)   --> GPIO20 (BCM, phys 38)
  3-Pin Side: Middle      --> GND (Any ground pin)
  2-Pin Side: Pin 1 (SW-) --> GND (Any ground pin)
  2-Pin Side: Pin 2 (SW+) --> GPIO23 (BCM, phys 16)

> [!TIP]
> **Prototyping with Jumper Wires**
> You do not have to plug the Display HAT directly onto the Pi. You can connect *everything* using standard Female-to-Female jumper wires. 
> - **Zero data pin conflicts:** The Display HAT and the external components use completely different GPIO data pins.
> - **No power splicing needed:** The Pi has two 5V pins (use one for the HAT, one for the Amp), two 3.3V pins, and eight GND pins (plenty for the HAT, Amp, and Encoder). You can run dedicated jumper wires for power/ground to every component without needing to splice or share wires.

Internal pull-ups via periph.io are sufficient for both encoder and HAT buttons.

Display HAT Mini buttons (built-in, no wiring):
  Button A (GPIO5)  --> software: Play/Pause
  Button B (GPIO6)  --> software: Menu
  Button X (GPIO16) --> software: Previous chapter
  Button Y (GPIO24) --> software: Next chapter

### 4.3 Firmware config.txt (device tree)

The RPi firmware assembles the device tree from `config.txt` plus the `overlays/` directory on the FAT partition (see §5.1). The complete, appliance-tuned config is stored in the repository at [system/boot/config.txt](file:///Users/jadams/code/github/jasonkradams/bedside-reader/system/boot/config.txt). Key lines: `dtparam=spi=on` / `dtparam=i2s=on` / `dtparam=audio=off`; the MAX98357A overlay `dtoverlay=max98357a,sdmode-pin=26`; and the panel overlay `dtoverlay=mipi-dbi-spi,spi0-1,speed=60000000` with `dtparam=width=320,height=240` and `dtparam=dc-gpio=9`.


### 4.4 Backlight control from Go

Backlight is a simple on/off, not a PWM duty. `display.SetBacklight(bool)` tries two paths in order. **First** it globs `/sys/class/backlight/*/brightness` and, if a backlight class device exists, writes `"1"`/`"0"` to it — the driver may name it `spi0.1`, `10-0045`, etc., so we glob rather than hardcode. **On this build there is no backlight class device**, so it falls through to the **fallback**: driving BCM **GPIO13** by raw sysfs. Because the sysfs GPIO number is `gpiochip_base + 13`, it reads the base from `/sys/class/gpio/gpiochip*/base` (defaulting to literal `13`), exports the line if needed, sets `direction=out`, then writes `value`.

```go
// internal/display/backlight.go (abbreviated)
package display

func SetBacklight(on bool) {
    val := "0"
    if on {
        val = "1"
    }

    // Preferred: a backlight class device, if the panel driver exposed one.
    matches, err := filepath.Glob("/sys/class/backlight/*/brightness")
    if err == nil && len(matches) > 0 {
        os.WriteFile(matches[0], []byte(val), 0644)
        return
    }

    // Fallback (the real path on this build): raw sysfs BCM GPIO13.
    gpioNum := "13"
    if m, err := filepath.Glob("/sys/class/gpio/gpiochip*/base"); err == nil && len(m) > 0 {
        if content, err := os.ReadFile(m[0]); err == nil {
            if base, err := strconv.Atoi(strings.TrimSpace(string(content))); err == nil {
                gpioNum = strconv.Itoa(base + 13)
            }
        }
    }
    gpioPath := "/sys/class/gpio/gpio" + gpioNum
    if _, err := os.Stat(gpioPath); os.IsNotExist(err) {
        os.WriteFile("/sys/class/gpio/export", []byte(gpioNum), 0200)
    }
    os.WriteFile(gpioPath+"/direction", []byte("out"), 0644)
    os.WriteFile(gpioPath+"/value", []byte(val), 0644)
}
```

Non-root access is granted declaratively in NixOS, not via a standalone udev file: a udev rule and the `bedside` service's `SupplementaryGroups`/`ExecStartPre` (which exports and chowns the GPIO line to the `gpio` group) are all defined in [system/configuration.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/system/configuration.nix).


### 4.5 HAT buttons + encoder in Go

All inputs are read identically through periph.io. The Display HAT Mini buttons are just GPIOs to ground — no library lock-in to Pimoroni's Python stack. The encoder is on **GPIO4 (A) / GPIO20 (B) / GPIO23 (push)** — not 17/22, which the RGB-LED lines use. Every pin is configured `PullUp, NoEdge`; the app **polls** rather than using edge interrupts (encoder A/B every 2ms, buttons every 5ms with a 50ms debounce).

```go
// internal/input/input.go (abbreviated)
package input

import (
    "time"

    "periph.io/x/conn/v3/gpio"
    "periph.io/x/conn/v3/gpio/gpioreg"
)

// HAT buttons A/B/X/Y = GPIO5/6/16/24; rotary encoder A/B/push = GPIO4/20/23.
func setup() {
    for _, name := range []string{
        "GPIO5", "GPIO6", "GPIO16", "GPIO24", // buttons
        "GPIO4", "GPIO20", "GPIO23", // encoder A, B, push
    } {
        pin := gpioreg.ByName(name)
        if pin == nil {
            continue // partially-connected HAT still works
        }
        pin.In(gpio.PullUp, gpio.NoEdge)
    }
}

// watchEncoder polls A/B; a falling edge on A with B high is clockwise (+1),
// otherwise counter-clockwise (-1).
func watchEncoder(pinA, pinB gpio.PinIO, emit func(delta int)) {
    lastA := pinA.Read()
    for {
        time.Sleep(2 * time.Millisecond)
        a, b := pinA.Read(), pinB.Read()
        if a != lastA {
            if a == gpio.Low && lastA == gpio.High {
                if b == gpio.High {
                    emit(1) // CW
                } else {
                    emit(-1) // CCW
                }
            }
            lastA = a
        }
    }
}
```

---

## 5. Software setup

### 5.1 OS image (declarative NixOS)

The OS is **NixOS**, not Raspberry Pi OS — the entire device is one declarative configuration. There is no `apt`, no `cloud-init`, no `userconf.txt`, no hand-written service files: kernel modules, DT overlays, the `bedside` service, users/groups, udev rules, and Wi-Fi are all expressed in Nix and built into a reproducible SD image.

- **Board / OS**: `aarch64-linux`, NixOS `stateVersion = "24.05"`, board module `raspberry-pi-3` (the Pi Zero 2 W is a BCM2837). Defined by `nixosConfigurations.bedside-reader` in [flake.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/flake.nix), importing the upstream `sd-image-aarch64.nix` module plus [system/configuration.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/system/configuration.nix) and [system/sd-image-opts.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/system/sd-image-opts.nix).
- **Firmware partition owns the device tree.** `useGenerationDeviceTree = false`; U-Boot ignores the NixOS FDTDIR, so `hardware.deviceTree.overlays` would never reach the kernel. Instead the RPi firmware assembles the DT from `config.txt` + `overlays/` on the FAT partition. `sd-image-opts.nix` appends `system/boot/config.txt` to the image's `firmware/config.txt`, copies `panel.bin`, and copies `${kernel}/dtbs/overlays` → `firmware/overlays`. The FAT partition is named **`BEDSIDEBOOT`** and `expandOnBoot = true` grows the root partition on first boot.
- **Build the image** with the `build-os` target (see [nix/packages.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/nix/packages.nix)): it runs a `nixos/nix` Docker container against a persistent store volume and emits `./result-img/…​.img.zst`.
- **Flash** (macOS) with `flash-os`: `zstdcat | dd` the `.img.zst` to the SD card, then inject Wi-Fi creds from `secrets/wireless.env` onto the `BEDSIDEBOOT` FAT partition as `wireless.env`.
- **Wi-Fi** is `wpa_supplicant` in imperative mode (`networking.wireless.enable`, interface `wlan0`, no in-Nix `networks` block). On boot a oneshot mounts `BEDSIDEBOOT`, sources `wireless.env` (`WIFI_SSID`/`WIFI_PASSWORD`), and writes `/etc/wpa_supplicant/wifi.conf`; a `wpa_supplicant-wlan0` override points `ExecStart` at that file. `wlan0` gets its address by DHCP. `openssh` is enabled for `deploy-os` / debugging.
- **Iterate without reflashing**: `deploy-os` runs `colmena apply` (inside a `nixos/nix` container) to push an incremental configuration change over SSH to the running device (`buildOnTarget = false`, so the build happens on the dev host).
- Silent, appliance-like boot is a property of the Nix config (kernel cmdline / `config.txt`), not a hand-edited `cmdline.txt` on the card.


### 5.2 Partition layout

The Nix SD image is the standard two-partition aarch64 layout; there is no separate app partition — `/var/lib/bedside` is a systemd `StateDirectory` on the root filesystem.

| Partition | FS    | Mount            | Mode              | Purpose                                                                                     |
| --------- | ----- | ---------------- | ----------------- | ------------------------------------------------------------------------------------------ |
| p1        | vfat  | (firmware)       | ro at runtime     | `BEDSIDEBOOT`: RPi firmware, `config.txt`, `overlays/`, `panel.bin`, `wireless.env`         |
| p2        | ext4  | /                | rw (nix store ro) | NixOS system + `/nix/store` (immutable) + the `bedside` binary; `expandOnBoot` grows it      |
| dir       | ext4  | /var/lib/bedside | rw                | bbolt (`library.db`), audiobook files, cover dir — `StateDirectory` + `ReadWritePaths`      |
| —         | ext4 (root) | /var/log   | rw                | journald flushes to SD every 1s (`SyncIntervalSec=1`) so logs survive an unplanned power-cut; not tmpfs today. `zramSwap` (150%) absorbs memory pressure separately. A tmpfs `/var/log` is a possible future hardening (planned) that would trade away that durability. |

### 5.3 Packages

No browser, no X, no package manager on the device. Runtime packages are declared in NixOS ([system/configuration.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/system/configuration.nix)); the important ones are tiny:

- **`mpv`** — the audio backend (uses its own bundled ffmpeg). Present system-wide and on the `bedside` unit's `path`.
- **`ffmpeg-headless`** — supplies **`ffprobe`** for the library scanner (duration/chapter metadata). It is on the `bedside` unit's `path` specifically because mpv's ffmpeg does not expose a standalone `ffprobe`.
- **ALSA** — `snd_bcm2835` is blacklisted so the MAX98357A I2S DAC is the only (and default, **card 0**) sound card. `/etc/asound.conf` pins the default PCM to `plug` → `hw:0,0` and the default control to `card 0`, though the app also names the card explicitly so it doesn't depend on that default.

A separate, minimal custom ffmpeg (`ffmpeg-aax`, built `--disable-everything` with just the codecs needed for lossless `.aax → .m4b` stream-copy decrypt) exists only for the offline `audible-convert` tool in [nix/packages.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/nix/packages.nix). It is **not** used by mpv or the scanner.

### 5.4 Boot ordering

There is exactly **one** service. No `graphical.target`, no display-manager, no second process to sequence — the Go binary both plays audio and draws the screen.

```
sound.target
     │
     v
bedside.service   (the Go binary: opens mpv, mmaps the panel fb, draws the UI)
     │
     v
multi-user.target
```

The unit is declared in NixOS ([system/configuration.nix](file:///Users/jadams/code/github/jasonkradams/bedside-reader/system/configuration.nix)), not as a hand-written `.service` file:

- `Type = notify` with `WatchdogSec = 30`; the Go binary calls `sd_notify(READY=1)` at startup and pings the watchdog thereafter.
- `ExecStart = ${bedside-app}/bin/bedside`, `Restart = always`, `RestartSec = 2`.
- `after = [ sound.target ]`, `wantedBy = [ multi-user.target ]`.
- Runs as user/group `bedside` with `SupplementaryGroups = [ video input gpio audio render ]` so it can touch the framebuffer, GPIO, and ALSA without root.
- `path = [ mpv ffmpeg-headless ]`; `StateDirectory = bedside` and `ReadWritePaths = /var/lib/bedside`.
- An `ExecStartPre` exports and chowns the backlight GPIO line to the `gpio` group.

**Why this is short**: the app opens `/dev/fbN` and the GPIO/ALSA devices directly; there is no window system, no login session, no display number, and nothing waiting on the network for a local socket.

### 5.5 Time-keeping (NTP only)

The Pi has no real-time clock and we've explicitly skipped the DS3231 to keep the build cheap and the BOM short. Time-keeping relies on NTP:

- This is NixOS, not Raspberry Pi OS: `systemd-timesyncd` is enabled by NixOS's own defaults (nothing in `configuration.nix` disables or overrides it) and syncs against the configured NTP servers once wifi is up. There's no `apt` and no Pi-OS-specific timesync package here.
- There is no RTC and no boot-time clock-persistence helper on this build (Raspberry Pi OS ships one of those as a Debian package; this NixOS image has no `apt` and doesn't include an equivalent). On a cold boot with no network yet, the clock sits wherever the kernel's default/last-known time lands until `timesyncd` corrects it.
- **If a power loss happens during a wifi outage**: the system clock is wrong until wifi recovers and `timesyncd` resyncs. Acceptable trade-off for the $20 we saved by skipping the DS3231.
- **No clock is rendered on the panel today.** `ui.go`'s `renderPlayer` draws title, chapter title, status line, chapter progress bar, and chapter/total time — no clock (see §6). A clock element, and gating it on a plausible-time check so it doesn't flash a 1970-ish date right after boot, is a **planned** UI addition, not implemented.
- **Adding a DS3231 later** is a 20-minute job: four-wire I2C breakout, one `dtoverlay=i2c-rtc,ds3231` line in `system/boot/config.txt`. Don't pre-optimize.

### 5.6 Position resume strategy

Two bbolt buckets do the work:

- **`progress`** — playback position per book, keyed by base filename, stored as a `%f` string. Written on progress ticks but **throttled to at most once every 10s** so the SD card isn't hammered; reset to 0 only on a true `eof`. Survives a power yank because bbolt fsyncs.
- **`system`** — a single `SystemState` (active file, `playing`, screen timeout, volume, encoder mode) that captures what the device should look like after a reboot.

On boot (`main.go`): read `SystemState`; if it names an active file, `LoadFile` it — which resumes the saved position — and restore volume, encoder mode, and screen timeout. `LoadFile` auto-plays, so the app immediately toggles pause back off **only if** the device was paused before shutdown; i.e. it faithfully restores the prior play/pause state rather than always blasting or always idling. With no saved state, it comes up idle in the library menu.

### 5.7 Screen timeout & wake

The bedside power-saver is a **screen timeout**, driven entirely by in-process `time.Timer`s in the App controller — no audio sleep timer, no server-push, no web UI.

- The timeout is a setting cycled from the library menu's **Settings row** (menu index 0): it steps through the ring `[Off, 1m, 5m, 15m, 60m]` (default 5m) and persists to `SystemState`.
- Any input resets the timer via `resetScreen`. When it fires it publishes a `"screen-timeout"` event; the handler blanks the backlight (`SetBacklight(false)`) — the **framebuffer contents are left intact**, only the light goes off.
- **Double-click the encoder** (two presses within 400ms) toggles the screen off/on manually. A single press (resolved after the 400ms double-click window) either cycles the timeout (on the Settings row), selects a book (on a book row), or toggles the encoder mode.
- Waking restores full backlight and re-arms the timeout. Encoder *rotation* deliberately does not wake the screen from a hard-off state; it just adjusts volume/scrub or scrolls the menu when the screen is on.

---

## 6. UI sketch (native framebuffer)

### 6.1 Screens

There are two screens, plus a hard-off state. The library menu is drawn as an overlay on top of the player screen, so "menu" is a flag, not a separate page.

| Screen           | Primary purpose                                                     | Affordances                                                                          |
| ---------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| **Player**       | Now-playing status: title, chapter, play state, progress, times    | Play/pause, skip chapter, encoder scrub/volume; idle title is "Bedside Audio"        |
| **Library menu** | Browse + pick a book; adjust settings                              | Row 0 = Settings (screen timeout); rows 1..N = books; encoder or X/Y scroll, press selects |
| **Screen-off**   | Backlight blanked after timeout or double-click (framebuffer kept) | Any button (or another double-click) wakes it back to full backlight                 |

### 6.2 Layout (Display HAT Mini, 320×240 landscape)

At 320×240 the UI is text and rectangles drawn with `basicfont.Face7x13`. The player screen stacks left-aligned lines at fixed Y offsets; the library menu dims the background and lists rows with marker prefixes. A small Wi-Fi signal icon sits at the top-right (green = up, red with an X = down). No cover art is rendered.

```
Player screen                             Library menu (overlay)
┌──────────────────────────────┐         ┌──────────────────────────────┐
│ Project Hail Mary        ▂▄▆ │  wifi   │                          ▂▄▆ │
│                              │         │  Library Menu                │
│ Chapter 14 - Eridian         │         │                              │
│                              │         │ > Settings: Screen Timeout   │
│ Playing  |  Vol: 45%         │         │      [5m]                    │
│                              │         │   Project Hail Mary          │
│ ██████████████░░░░░░░░░░░░░░ │  (bar)  │ * Cryptonomicon              │
│                              │         │   The Three-Body Problem     │
│ Chap: 06:42 / 18:03          │         │   Educated                   │
│ Total: 01h12m / 09h48m       │         │                              │
└──────────────────────────────┘         └──────────────────────────────┘

Status line reads "Idle" / "Playing" /     Markers: "> " selected row,
"Paused", then "| Vol: N%" in volume        "* " the book currently loaded,
mode or "| Mode: Scrub" in scrub mode.      "  " otherwise.

HAT buttons                               Rotary encoder
  A (GPIO5)  = Play / Pause                  Turn (vol mode)   = volume +/- 5
  B (GPIO6)  = toggle Library menu           Turn (scrub mode) = seek +/- 15s
  X (GPIO16) = skip chapter back /           Turn (in menu)    = scroll rows
               scroll menu down              Press (single)    = select / cycle
  Y (GPIO24) = skip chapter fwd /                                timeout / toggle mode
               scroll menu up                Press (double)    = screen off / on
```

### 6.3 Render pipeline

`render()` locks the canvas, clears it to the background color, draws the player screen, overlays the menu when active, stamps the Wi-Fi icon, then packs the RGBA canvas into RGB565 straight into the `mmap`. It is invoked by the bus subscriber (`listen`) on state changes and by the Wi-Fi poller on link changes.

```go
// internal/ui/ui.go (abbreviated, faithful to the real code)

func (r *Renderer) render() {
    r.mu.Lock()
    defer r.mu.Unlock()

    // 1. Clear background (dark blue).
    draw.Draw(r.canvas, r.canvas.Bounds(),
        &image.Uniform{colorBackground}, image.Point{}, draw.Src)

    // 2. Player screen, then the menu overlay if it's open.
    r.renderPlayer()
    if r.menuState.Active {
        r.renderMenu()
    }
    r.drawWiFiIcon(300, 10, r.wifiConnected)

    // 3. Pack RGBA -> RGB565 and write to the mmapped framebuffer.
    copyToRGB565(r.mmap, r.canvas)
}

// Text is stamped with basicfont via a font.Drawer.
func addLabel(img *image.RGBA, x, y int, label string, col color.RGBA) {
    d := &font.Drawer{
        Dst:  img,
        Src:  image.NewUniform(col),
        Face: basicfont.Face7x13,
        Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
    }
    d.DrawString(label)
}

// RGB565, little-endian, packed directly into the framebuffer bytes.
func copyToRGB565(dst []byte, src *image.RGBA) {
    // ... for each pixel:
    //   r5 := uint16(r) >> 3; g6 := uint16(g) >> 2; b5 := uint16(b) >> 3
    //   rgb565 := (r5 << 11) | (g6 << 5) | b5
    //   dst[o] = byte(rgb565); dst[o+1] = byte(rgb565 >> 8)
}
```

`renderPlayer` draws the title at y=30, chapter title at y=70, the status line at y=110, a 300px-wide chapter progress bar at y=150, and the `Chap:`/`Total:` time lines at y=180 and y=200. `renderMenu` fills a dim overlay, draws the "Library Menu" header, then the Settings row and book rows starting at y=70 with a 25px row pitch, scrolling so the selected row stays visible.

### 6.4 Input → event → effect

Input watchers publish typed events onto the bus; the App controller (`app.go`) turns them into player/menu actions and republishes state, which the renderer repaints. There are no HTTP endpoints — this table is the whole control surface.

| Event                     | Source                     | Effect                                                                          |
| ------------------------- | -------------------------- | ------------------------------------------------------------------------------- |
| `EventButtonPlayPause`    | HAT button A               | Wake if off; else in menu select the highlighted book, else toggle pause.       |
| `EventButtonMenu`         | HAT button B               | Toggle the library menu; on open, highlight the currently-playing book.         |
| `EventButtonSkipBack`     | HAT button X               | In menu scroll down (index++); else `SkipChapter(-1)`.                          |
| `EventButtonSkipFwd`      | HAT button Y               | In menu scroll up (index--); else `SkipChapter(+1)` unless at the last chapter. |
| `EventEncoderTurn` (±1)   | encoder A/B poll           | In menu scroll; else scrub ±15s (scrub mode) or volume ±5 (vol mode).           |
| `EventEncoderBtn`         | encoder push               | Single (post-400ms): cycle timeout / select book / toggle encoder mode. Double (<400ms): screen off/on. |
| `screen-timeout`          | `time.Timer`               | Blank the backlight; framebuffer contents are retained.                          |
| `EventPlayerStateChanged` / `EventPlayerProgressTick` | Player | Renderer repaints the player screen with new position/state.                    |
| `EventMenuUpdate`         | App controller             | Renderer repaints (or clears) the menu overlay.                                  |

---

## 7. Bedside-specific gotchas

### 7.1 True-off backlight

Backlight off is `SetBacklight(false)` → `"0"` written to the backlight class device if one exists, otherwise to BCM GPIO13 via sysfs (the real path on this build). Verify in a dark room before final assembly that it fully extinguishes the LEDs. If you see residual glow, add a P-channel MOSFET on the backlight power rail driven by a free GPIO and you have a hard kill.

### 7.2 Wake-on-button from screen-off

There's nothing to blank or relaunch — the framebuffer stays mapped and its contents intact; only the light goes out. On timeout or a double-click the app calls `SetBacklight(false)`; on wake it calls `SetBacklight(true)` and re-arms the timeout. Encoder rotation deliberately does not wake from a hard-off state (avoid accidental brushes); button presses do, and the encoder push counts as a button.

### 7.3 Screen-off vs. always-on

The current build has one power-saving state: backlight fully off (see §5.7). It is not a dimmed clock mode — the panel goes dark and the framebuffer is simply retained. A future dim/clock-only mode (backlight at a low level with a reduced screen) would slot in here as an extra branch, but the code today toggles between full-on and off.

### 7.4 Brownout / SD-card protection

- **Root filesystem is read-write ext4 today** (`fileSystems."/"` in `configuration.nix` sets no `ro` option). Only `/nix/store` is immutable — a property of Nix itself, not a mount flag. A fully read-only root is a possible future hardening (planned) if SD wear becomes a real problem.
- **journald flushes to SD every 1 second** (`SyncIntervalSec=1`) specifically so logs survive an unplanned power-cut — the opposite of overlaying tmpfs. Trading that durability for a tmpfs `/var/log` (fewer SD writes, logs lost on a crash) is a possible future hardening (planned), not configured today.
- **bbolt on rw partition** with `sync` mount option. Position writes are tiny; the SD endurance card handles them fine.
- **Watchdog**: enable BCM2837 hardware watchdog in `config.txt` (`dtparam=watchdog=on`) and `WatchdogSec=30` on `bedside.service`. If the `bedside` binary hangs, the hardware reboots.
- **Undervoltage flags**: monitor `vcgencmd get_throttled` in a tiny exporter to journald. Use a quality USB cable; bedside HDMI capture during testing eats current.

### 7.5 Fan-free thermal design

- Pi Zero 2 W idles at 45–55°C in a closed plastic case at 22°C ambient. Spoken-word playback is near-idle CPU load; the native renderer only repaints the 320×240 framebuffer on state changes, so the UI's CPU cost is negligible — there is no browser or layout engine burning cycles.
- Display HAT Mini stacks directly on the GPIO header, sitting above the SoC. Idle thermals don't require a heatsink. If you ever load the device with anything heavier (don't), drill a few vent slots above the SoC in the enclosure.
- Hardwood final enclosure: cut a hidden vent slot at the back.

### 7.6 Boot UX

- Plymouth splash with a single static image (cover-art-style). No spinner — it looks like consumer firmware, not a Pi booting.
- `disable_overscan=1` even though we're SPI — sets a clean baseline for the framebuffer.
- You could disable the rainbow boot square by adding `disable_splash=1` to `config.txt` — it is not currently set there.

### 7.7 Audio gotchas

- MAX98357A has a small turn-on transient (click) when the amp first comes out of shutdown. On this build the amp's SD/enable pin is **GPIO26**, owned by the **kernel `max98357a` driver** via the DT overlay (`dtoverlay=max98357a,sdmode-pin=26`) and toggled automatically by ALSA/DAPM on stream start/stop — the app deliberately does **not** touch GPIO26 (driving it from userspace would race the kernel and mute audio). To mask pops the player instead mutes on file transitions and unmutes on `file-loaded`, and keeps mpv running idle (`--idle`) so the pipeline stays warm.
- ALSA volume ceiling: not configured today. `/etc/asound.conf` (§5.3) only pins the default PCM/ctl to card 0, and the app clamps its own volume to 0–100% in software (`handleVolumeChange`) with no cap below that. If a fat-fingered encoder spin at 3am worries you, you could add a hard ceiling in `/etc/asound.conf` (e.g. map UI 0–100% to ALSA 0–80% of nominal) — that's a suggestion, not current behavior.
- Sample-rate mismatch: Audiobook files may be 22.05 / 44.1 / 48 kHz mixed in a single book. Let mpv resample; don't try to set the ALSA device rate.
- **Speaker-driver sizing reality check**: CE32A-4 is a 1.25" full-range. It will not deliver chest-thumping bass — it doesn't need to. Spoken word lives in 200Hz–6kHz and the CE32A-4 covers that range cleanly. If you find yourself wanting more bottom-end in evaluation, a small (~30 cubic inch) sealed enclosure with a fistful of polyfill helps; do not chase the missing low end by EQ — you'll just bottom out the driver and clip.
- **Mono is fine for spoken word at this scale.** The MAX98357A is mono by design; if you ever want stereo, you wire two of them in parallel (one set to left channel, one to right via the SEL pin) and add a second CE32A-4. That's a $20 upgrade you can do later without ripping anything out.

### 7.8 Privacy / scope

- No microphone. No speech assistant. No phone pairing. The bedroom stays a phone-free zone — that's the whole point.
- Local network only. The device only connects to wifi for NTP time sync and, when loading books, an SSH session (`scp`/`rsync`). No outbound calls, no telemetry. (A Samba share for drag-and-drop transfers is planned, not configured.) Block egress at your router if you want belt-and-suspenders.

---

## 8. Build roadmap

| Phase | Milestone            | Definition of done                                                                                                                                    |
| ----- | -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| **0** | Bench rig            | Pi Zero 2 W + Display HAT Mini + MAX98357A breadboarded with a single CE32A-4 speaker. Plays a hardcoded local M4B; backlight goes to zero on demand. |
| **1** | Go skeleton          | Event bus wired; the Go binary `mmap`s the panel framebuffer and draws the player screen; pressing HAT Button A pauses playback.                       |
| **2** | Local Library Scan   | `ffprobe` scans `/var/lib/bedside/audiobooks`, populates `boltdb`, and mpv plays local files directly.                                                |
| **3** | Library UI           | Browse + search via encoder; cover art rendering at 480px square.                                                                                     |
| **4** | Now-playing complete | Chapter changes (prev/next), sleep timer, real-time progress.                                                                                                |
| **5** | Bedside polish       | Backlight modes, clock-only, wake-on-button, read-only root, watchdog.                                                                                |
| **6** | Prototype enclosure  | PETG box; speaker baffle tuned; live with it for two weeks.                                                                                           |
| **7** | Final hardwood       | Once you've found what's wrong with the prototype.                                                                                                    |

_End of document._
