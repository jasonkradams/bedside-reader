# Bedside Audiobook Appliance

**Architecture, BOM, and Build Plan**
_Go + Datastar + Pi Zero 2 W + Display HAT Mini + MAX98357A + CE32A-4 (mono)_

---

## 0. Decisions locked

**This is the Tier 2 lean build.** Earlier revisions of this doc went chasing premium components — Pi 4, 5" Touch Display, stereo speakers, DS3231 RTC, MiniAmp DAC HAT — most of which weren't earning their cost for a bedside spoken-word player. This rev strips back to the minimum that delivers the experience: standalone bedside operation, screen for clock + browsing + now-playing, audio that fills a quiet bedroom.

| Axis         | Choice                                                                     | Why it matters downstream                                                                                                                                                                 |
| ------------ | -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| SBC          | **Raspberry Pi Zero 2 W**                                                  | $15 board, fanless idle, fits the smallest enclosure. Cog + Go + Datastar runs comfortably; we're not Chromium-bound anymore.                                                             |
| Display      | **Pimoroni Display HAT Mini** (2.0" IPS SPI + 4 onboard buttons + RGB LED) | Stacks directly on the Pi header. Replaces the separate display + buttons + bezel work. Software-controllable backlight to true zero.                                                     |
| Audio        | **Adafruit MAX98357A + 1× Dayton CE32A-4** (mono)                          | $6 I2S amp breakout instead of a $32 HAT. Single 1.25" 4Ω driver. Mono for spoken word is fine — at this driver size and bedside distance you wouldn't perceive much stereo image anyway. |
| Clock source | NTP only (no DS3231 RTC)                                                   | Saves $20 + I2C wiring. Accept that the clock reads briefly wrong after a power-loss-plus-wifi-down event. Easy to add later as a $3 breakout.                                            |
| Sync         | Standalone / Offline-first                                                 | Completely decoupled from Audiobookshelf. Audiobooks are loaded via Samba (SMB) share over Wi-Fi onto local storage.                                                                      |
| **Renderer** | **Cog (WebKitGTK) on KMS/DRM** — no Xorg, no Chromium                      | ~150MB resident vs Chromium's ~400MB. Boots 4–6s faster. Datastar unchanged.                                                                                                              |

**Estimated total delivered cost: ~$135** (~$77 unique-to-this-build parts).

---

## 1. Architecture

Single Go binary on the Pi. It owns audio playback, GPIO input, backlight, the library scanner, position state, and HTTP/SSE to a kiosk WebKit (Cog) pointed at localhost. Datastar drives the UI: server-rendered HTML, attribute-driven interactivity, and SSE for everything that changes over time. There is no client-side state framework — no React, no htmx-plus-Alpine. Datastar attributes hang off the same DOM the server rendered.

**Renderer:** [Cog](https://github.com/Igalia/cog) (Igalia's WebKit kiosk shell) on top of [WebKitGTK](https://webkitgtk.org/), rendered directly to KMS/DRM with no Xorg. This is intentionally not Chromium: a single-tab kiosk is exactly what Cog was built for, the resident memory is roughly a third, and skipping Xorg removes a whole boot-time stratum.

### 1.1 Component map

Components within the Go binary, all coordinated through an in-process event bus (channels).

| Component       | Responsibility                                                                                               | Inputs                  | Outputs                            |
| --------------- | ------------------------------------------------------------------------------------------------------------ | ----------------------- | ---------------------------------- |
| **Player**      | mpv IPC client; play/pause/seek/volume; emits position ticks at 1Hz                                          | Commands from event bus | PlaybackState events               |
| **Library**     | Local filesystem scanner using `ffprobe` (via `ffmpeg`) to extract metadata, chapters, and cover art         | Local audio files       | Library DB updates, cover blobs    |
| **Media Loader**| Samba (SMB) share runs on Wi-Fi connection to allow easy drag-and-drop file transfers from any OS            | SMB over Wi-Fi          | File writes to local storage       |
| **Input**       | GPIO: rotary encoder (quadrature decode), HAT buttons (debounced); push events to bus                        | periph.io GPIO          | InputEvent (rotate, click, button) |
| **Display**     | Backlight PWM via /sys/class/backlight; modes: full / dim / off-but-clock / fully-off; wake-on-button        | InputEvent, idle timer  | BacklightState events              |
| **Sleep timer** | Countdown + fade-out + pause; SSE-driven progress to UI                                                      | Timer commands, Player  | TimerTick events, Player commands  |
| **Web**         | HTTP routes, templ-rendered HTML, Datastar SSE endpoint, action handlers                                     | All bus events          | HTML to Chromium                   |

### 1.2 Event flow (state to browser)

State flows one direction: hardware/timers → event bus → SSE merge fragments → DOM. UI actions flow back via Datastar `data-on-*` attributes hitting POST endpoints that emit bus commands.

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
        │   Player     │── mpv IPC --> libmpv --> │  MAX98357A   │
        │              │                          │  I2S breakout│
        └──────────────┘                          └──────┬───────┘
                │                                        │ I2S
                v                                        v
        ┌──────────────┐                          ┌──────────────┐
        │   Web/SSE    │── merge-fragments ----->│  Cog kiosk   │
        │   (Datastar) │   merge-signals          │  (WebKitGTK  │
        │              │                          │   on KMS/DRM)│
        └──────────────┘                          └──────────────┘
```

### 1.3 Why Datastar fits this

- **Hypermedia stays the source of truth.** The Pi is bandwidth-rich (localhost) and the screen is small. Server-rendered fragments cost nothing and keep all state in Go.
- **SSE is the right transport for a player.** Position, chapter changes, sleep-timer countdown, and clock all push from the server. No polling, no WebSocket framing.
- **`data-on-click` to POST** keeps button handlers trivial. The server emits a bus command and pushes a fragment back over SSE — the click endpoint can return 204.
- **No SPA** means no `npm`, no bundler, no source maps to debug at 2am when the device hangs.

### 1.4 Renderer choice: why Cog/WebKitGTK over Chromium

Chromium is the heaviest single thing on this device. For a one-tab, one-origin kiosk that never browses the open web, almost every Chromium feature is dead weight: tab management, the multi-process sandbox tree, the GPU process, the network service process, extensions, sync, OOPIF, V8 isolates beyond one. The cost is ~400MB resident, multi-second startup, and an Xorg dependency.

| Renderer                       | Resident RAM | Boots in | Server-side render?     | Verdict                                |
| ------------------------------ | ------------ | -------- | ----------------------- | -------------------------------------- |
| **Cog + WebKitGTK on KMS/DRM** | ~140–180 MB  | ~2–3s    | Yes, Datastar unchanged | **Pick this**                          |
| Chromium kiosk                 | ~380–450 MB  | ~7–10s   | Yes                     | Original plan; overkill                |
| Firefox/GeckoView              | ~300 MB      | slower   | Yes                     | No win over WebKit                     |
| Servo                          | varies       | varies   | Yes                     | Too immature for a daily-use appliance |

Cog is purpose-built for set-top-box / infotainment kiosk use cases by Igalia (the WebKit folks). It runs against a `--platform=drm` backend so we render straight to the framebuffer through KMS — no Xorg, no Wayland compositor required. Datastar is plain HTML+JS; WebKit's JavaScriptCore runs Datastar's tiny client just fine.

### 1.5 Why Pi Zero 2 W is enough

With Chromium gone, the Zero 2 W's 512MB RAM is not the limiting factor it used to be. Idle budget with Cog + Go server + mpv lands around 220–260MB resident. CPU is light for spoken-word playback and Datastar fragment patches; the only thing that would stress this SBC is H.264 video cover decode (some M4B books carry one) — we avoid runtime decoding by extracting a static JPEG of the cover using `ffprobe`/`ffmpeg` during the scan phase.

**Power**: a Zero 2 W under this load draws roughly 500–800mA at 5V — comfortable on a 2.5A micro-USB PSU with the MAX98357A on the same supply. No PSU oversizing needed.

**Thermal**: idle 45–55°C in a closed plastic case at 22°C ambient, no throttling. The Pi 4 heatsink case we previously specced was overkill — we don't need it.

---

## 2. Go library recommendations

### 2.1 Audio playback

**Use mpv via JSON IPC, not a Go-native decoder.** This is the controversial pick so I'll defend it. `beep` and `oto` decode in Go and are great for games and effects, but they don't handle: gapless chapter transitions across M4B parts, M4B chapter metadata, accurate seek across VBR MP3, ReplayGain, or buffered HTTP streaming. Reimplementing those is a months-long detour.

mpv handles all of it, runs as a child process, and exposes a stable JSON-over-unix-socket protocol. You connect, send `{"command":["set_property","pause",false]}`, observe properties for `time-pos` and `chapter`, and you're done.

| Option                                                    | Verdict        | Notes                                                                                                             |
| --------------------------------------------------------- | -------------- | ----------------------------------------------------------------------------------------------------------------- |
| **mpv + JSON IPC** (Recommended)                          | Use this       | Spawn `mpv --idle --input-ipc-server=/run/abs/mpv.sock --no-video --really-quiet`. Tiny Go client; ~200 lines.    |
| **libmpv via cgo** (`gen2brain/go-mpv` or write your own) | Acceptable     | Tighter integration, no subprocess. Adds cgo build complexity for marginal gain on a Pi.                          |
| **beep / oto**                                            | Skip for this  | No M4B chapter handling, no gapless, you'd be writing a player not an appliance.                                  |
| **MPRIS over D-Bus** (`godbus/dbus`)                      | Optional layer | Nice if you want phone-as-remote later. Add as a thin adapter on top of the Player; not the primary control path. |

### 2.2 GPIO

| Library                                                     | Verdict    | Notes                                                                                                                |
| ----------------------------------------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------------- |
| **periph.io/x/conn/v3 + periph.io/x/host/v3** (Recommended) | Use this   | Actively maintained, idiomatic Go, edge-trigger interrupts via gpiocdev backend. Handles encoder quadrature cleanly. |
| `stianeikeland/go-rpio`                                     | Avoid      | mmaps `/dev/mem`, no edge interrupts, polls. Encoders will skip detents at wake-from-idle.                           |
| `warthog618/go-gpiocdev`                                    | Acceptable | Modern character-device API. Lower-level than periph.io. Use directly if you want zero abstraction.                  |

### 2.3 Local Library Scanner

Instead of an API client, the appliance scans the local directory (`/var/lib/bedside/audiobooks/`) directly. We rely on **`ffprobe`** (part of `ffmpeg`) to handle metadata extraction because it handles M4B chapters correctly, avoiding the need for complex Go tagging libraries.

A Go goroutine runs periodically or on-demand:
1. Walks the directory looking for `.m4b`, `.mp3`, `.m4a`.
2. Runs `ffprobe -v quiet -print_format json -show_format -show_chapters -show_streams /path/to/file` and unmarshals the JSON.
3. Updates the local `boltdb` library catalog.
4. Uses `ffmpeg` to extract cover art embedded in the file to `/var/lib/bedside/covers/{fileHash}.jpg`.

### 2.4 Datastar Go SDK + templating

Datastar publishes a Go SDK ([github.com/starfederation/datastar](https://github.com/starfederation/datastar)) with helpers for SSE: `MergeFragments`, `MergeSignals`, `RemoveFragments`, `ExecuteScript`. Pair it with [a-h/templ](https://github.com/a-h/templ) for type-safe HTML. templ compiles .templ files to Go, so you get IDE completion and compile-time checks on your hypermedia.

| Layer                 | Library                                                               | Notes                                                 |
| --------------------- | --------------------------------------------------------------------- | ----------------------------------------------------- |
| HTML rendering        | [a-h/templ](https://github.com/a-h/templ)                             | Type-safe components; `templ generate` in your build. |
| SSE / Datastar        | [starfederation/datastar](https://github.com/starfederation/datastar) | `sse.MergeFragments(w, html)`; handles event framing. |
| HTTP router           | [go-chi/chi/v5](https://github.com/go-chi/chi)                        | Minimal, idiomatic, plays well with templ handlers.   |
| Audio                 | [mpv (JSON IPC)](https://mpv.io/manual/stable/#json-ipc)              | Spawn as a subprocess; control via unix socket.       |
| GPIO                  | [periph.io/x/conn/v3](https://periph.io/)                             | Idiomatic Go, edge interrupts via gpiocdev backend.   |
| Metadata extraction   | `os/exec` wrapping `ffprobe`                                          | Extract chapters and tags directly from media files.  |
| Storage               | [go.etcd.io/bbolt](https://github.com/etcd-io/bbolt)                  | Single-file KV; atomic writes survive yanked power.   |
| Image processing      | [disintegration/imaging](https://github.com/disintegration/imaging)   | Resize cover art to 480px once on download.           |
| Logging               | [stdlib log/slog](https://pkg.go.dev/log/slog)                        | JSON to journald, structured.                         |
| Hot reload (dev only) | [air-verse/air](https://github.com/air-verse/air)                     | Don't ship it; dev convenience.                       |

### 2.5 Other useful bits

- **Cover art cache**: store JPEGs on disk under `/var/lib/bedside/covers/{fileHash}.jpg`. Use `ffmpeg` to extract the embedded cover art during the library scan. Decode/resize once with `disintegration/imaging` to a 480px square.
- **Position persistence**: `boltdb` (`go.etcd.io/bbolt`) — single file, no daemon, atomic writes survive yanked power. Bucket: `progress`, key: `fileHash`, value: JSON of `{position, chapterIdx, updatedAt}`. Flush on every SSE tick (1Hz). The `library` bucket also stores the full metadata catalog.
- **Search**: a simple `strings.Contains` over normalized titles in a goroutine over the `boltdb` library catalog.

---

## 3. Bill of materials

**Single lean build.** No more A/B testing across two SBCs — earlier revisions kept a Pi 4 + Touch Display 2 alongside a Zero 2 W + Display HAT Mini for comparison. Once the renderer was switched to Cog there was no reason to spend Pi 4 money, so we're committing to the smaller, cheaper path. Total delivered cost lands around **$135**, with about **$77** of that being unique-to-this-build parts (the rest are PSU/SD/jumpers/case that you might already own).

### 3.1 Compute

| Item    | Part                                                                                                                   | Supplier  | Qty | Delivered | Notes                                                                                                                                    |
| ------- | ---------------------------------------------------------------------------------------------------------------------- | --------- | --- | --------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| SBC     | [Raspberry Pi Zero 2 W (with headers pre-soldered)](https://www.pishop.us/product/raspberry-pi-zero-2-wh/)             | PiShop.us | 1   | ~$24      | Pre-soldered headers ($3 premium, worth it). Headerless is $15 + soldering 40 pins.                                                      |
| microSD | [SanDisk High Endurance 32GB](https://www.amazon.com/SanDisk-Endurance-microSDXC-Adapter-Monitoring/dp/B07P3D6Y5B)     | Amazon    | 1   | ~$10      | Endurance class matters because we'll write position state ~once/sec. 32GB is plenty — Pi OS Lite + Go binary + cover-art cache is <2GB. |
| PSU     | [CanaKit 2.5A micro-USB PSU (UL listed)](https://www.amazon.com/CanaKit-Raspberry-Supply-Adapter-Listed/dp/B00MARDJZ4) | Amazon    | 1   | ~$10      | Zero 2 W uses micro-USB power, not USB-C. 2.5A absorbs MAX98357A current transients comfortably.                                         |
| Case    | [Basic Pi Zero 2 W plastic case](https://www.amazon.com/s?k=raspberry+pi+zero+2+w+case)                                | Amazon    | 1   | ~$6       | Generic two-piece snap case. We're not heatsinking — idle thermals are fine in a closed plastic shell.                                   |

### 3.2 Display

| Item                    | Part                                                                                      | Supplier  | Qty | Delivered | Notes                                                                                                                                                                       |
| ----------------------- | ----------------------------------------------------------------------------------------- | --------- | --- | --------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Display + buttons + LED | [**Pimoroni Display HAT Mini (PIM589)**](https://www.pishop.us/product/display-hat-mini/) | PiShop.us | 1   | ~$30      | 2.0" 320×240 IPS SPI. **Four onboard tactile buttons** (A/B/X/Y on GPIO 5/6/16/24) + RGB LED — replaces a separate button board entirely. Stacks directly on the Pi header. |

### 3.3 Audio

| Item             | Part                                                                                                                                   | Supplier      | Qty | Delivered | Notes                                                                                                                                            |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------- | ------------- | --- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| I2S amp breakout | [**Adafruit MAX98357A** I2S Class-D Mono Amp](https://www.adafruit.com/product/3006)                                                   | Adafruit      | 1   | ~$13      | $6 + ~$7 USPS. 3W mono at 4Ω from a tiny breakout. Talks I2S to GPIO 18/19/21. Compatible with the standard `hifiberry-dac` device-tree overlay. |
| Speaker driver   | [**Dayton Audio CE32A-4** 1.25" Mini Speaker, 4Ω](https://www.parts-express.com/Dayton-Audio-CE32A-4-1-1-4-Mini-Speaker-4-Ohm-285-103) | Parts Express | 1   | ~$15      | Single driver, mono. Laptop-scale full-range. $5 driver + $9.95 PE flat ground. Living with mono first; add a second later if you miss stereo.   |

### 3.4 Controls

| Item                  | Part                                                                                                            | Supplier        | Qty | Delivered | Notes                                                                                                                                                                           |
| --------------------- | --------------------------------------------------------------------------------------------------------------- | --------------- | --- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Rotary encoder + push | [Generic EC11 24-detent encoder w/ pushbutton](https://www.amazon.com/s?k=ec11+rotary+encoder+with+push+button) | Amazon (5-pack) | 1   | ~$8 for 5 | Generic EC11 from a multipack is fine for a one-off build; same form factor and same connections as the Bourns PEC11R.                                                          |
| Encoder knob          | [Aluminum 20mm D-shaft or round-shaft knob](https://www.amazon.com/s?k=aluminum+knob+20mm+6mm)                  | Amazon          | 1   | ~$5       | Any aluminum knob that fits a 6mm shaft. Adafruit #5527 ($3 + Adafruit shipping = ~$13) is the pretty option; a 4-pack on Amazon is $5 if you can live with anonymous aluminum. |

**HAT buttons replace the discrete button bank.** Map them in software: A=Play/Pause, B=Back, X=Skip-30, Y=Skip+30. The rotary encoder handles scrolling the library list and adjusting volume.

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
| GPIO5             | 29   | **Display HAT Mini: Button A**              | Map to Play/Pause.                                                       |
| GPIO6             | 31   | **Display HAT Mini: Button B**              | Map to Back.                                                             |
| GPIO8 (CE0)       | 24   | **Display HAT Mini: SPI CE0**               | ST7789 chip-select.                                                      |
| GPIO9 (MISO)      | 21   | **Display HAT Mini: DC**                    | Repurposed as data/command.                                              |
| GPIO10 (MOSI)     | 19   | **Display HAT Mini: SPI MOSI**              |                                                                          |
| GPIO11 (SCLK)     | 23   | **Display HAT Mini: SPI clock**             |                                                                          |
| GPIO13            | 33   | **Display HAT Mini: backlight PWM**         | Hardware PWM channel.                                                    |
| GPIO16            | 36   | **Display HAT Mini: Button X**              | Map to Skip-30.                                                          |
| GPIO17            | 11   | **Rotary encoder A**                        | Free GPIO; edge-triggered.                                               |
| GPIO18 (PCM_CLK)  | 12   | **MAX98357A: BCLK**                         | I2S bit clock.                                                           |
| GPIO19 (PCM_FS)   | 35   | **MAX98357A: LRC**                          | I2S word select.                                                         |
| GPIO21 (PCM_DOUT) | 40   | **MAX98357A: DIN**                          | I2S data.                                                                |
| GPIO22            | 15   | **Rotary encoder B**                        | Free GPIO.                                                               |
| GPIO23            | 16   | **Rotary encoder push**                     | Free GPIO.                                                               |
| GPIO24            | 18   | **Display HAT Mini: Button Y**              | Map to Skip+30.                                                          |
| GPIO25            | 22   | **Display HAT Mini: Reset**                 | ST7789 reset line.                                                       |
| GPIO27            | 13   | **Display HAT Mini: RGB LED (one channel)** | Multi-channel LED via PWM on shared pins; details in Pimoroni schematic. |

### 4.2 Wiring diagram

Because the **Display HAT Mini** plugs directly into the entire 40-pin header, the external components (Audio Amp and Rotary Encoder) must be wired by either soldering to the underside of the Pi's GPIO pins, or by using an extra-tall "stacking header" that lets the pins protrude through the Display HAT.

Here is the exact mapping of the 40-pin header to the external components:

```text
                     Raspberry Pi 40-Pin Header
                             (Top View)
                           [micro-SD side]
      
                +3.3V  [ 1]  [ 2]  5V ----------> MAX98357A (Vin)
         SDA (GPIO 2)  [ 3]  [ 4]  5V 
         SCL (GPIO 3)  [ 5]  [ 6]  GND ---------> MAX98357A (GND)
             (GPIO 4)  [ 7]  [ 8]  TXD
                  GND  [ 9]  [10]  RXD
Encoder A   (GPIO 17)  [11]  [12]  (GPIO 18) ---> MAX98357A (BCLK)
       [HAT LED]       [13]  [14]  GND ---------> Encoder (GND / Common)
Encoder B   (GPIO 22)  [15]  [16]  (GPIO 23) ---> Encoder (SW+ / Push)
                 3.3V  [17]  [18]  (GPIO 24) [HAT Button Y]
       [HAT SPI MOSI]  [19]  [20]  GND ---------> Encoder (SW- / Push GND)
       [HAT SPI DC]    [21]  [22]  (GPIO 25) [HAT Reset]
       [HAT SPI SCLK]  [23]  [24]  (GPIO 8)  [HAT SPI CE0]
                  GND  [25]  [26]  (GPIO 7)
                ID_SD  [27]  [28]  ID_SC
[HAT Button A]         [29]  [30]  GND
[HAT Button B]         [31]  [32]  (GPIO 12)
[HAT Backlight PWM]    [33]  [34]  GND
MAX98357A (LRC) <---   [35]  [36]  (GPIO 16) [HAT Button X]
             (GPIO 26) [37]  [38]  (GPIO 20)
                  GND  [39]  [40]  (GPIO 21) ---> MAX98357A (DIN)

                         [USB / HDMI side]
```

MAX98357A breakout wiring:
  Vin    --> Pi 5V  (header pin 2 or 4)
  GND    --> Pi GND (header pin 6, 9, 14, ...)
  DIN    --> GPIO21 (PCM_DOUT, physical pin 40)
  BCLK   --> GPIO18 (PCM_CLK,  physical pin 12)
  LRC    --> GPIO19 (PCM_FS,   physical pin 35)
  SD     --> leave floating (always-on; do NOT tie to GND or amp mutes)
  GAIN   --> leave floating (default 9dB)
  + / -  --> CE32A-4 speaker terminals

Rotary encoder (generic EC11):
  A      --> GPIO17 (BCM, phys 11)
  B      --> GPIO22 (BCM, phys 15)
  COMMON --> GND
  SW+    --> GPIO23 (BCM, phys 16)
  SW-    --> GND

> [!TIP]
> **Prototyping with Jumper Wires**
> You do not have to plug the Display HAT directly onto the Pi. You can connect *everything* using standard Female-to-Female jumper wires. 
> - **Zero data pin conflicts:** The Display HAT and the external components use completely different GPIO data pins.
> - **No power splicing needed:** The Pi has two 5V pins (use one for the HAT, one for the Amp), two 3.3V pins, and eight GND pins (plenty for the HAT, Amp, and Encoder). You can run dedicated jumper wires for power/ground to every component without needing to splice or share wires.

Internal pull-ups via periph.io are sufficient for both encoder and HAT buttons.

Display HAT Mini buttons (built-in, no wiring):
  Button A (GPIO5)  --> software: Play/Pause
  Button B (GPIO6)  --> software: Back / Menu
  Button X (GPIO16) --> software: Skip -30s
  Button Y (GPIO24) --> software: Skip +30s

### 4.3 /boot/firmware/config.txt

The complete and optimized configuration is stored in the repository at [boot/config.txt](file:///Users/jasonadams/code/github/jasonkradams/bedside-clock/boot/config.txt).


### 4.4 Backlight control from Go

The Display HAT Mini's backlight is a PWM line on GPIO13. The `mipi-dbi-spi` overlay exposes it through `/sys/class/backlight/.../brightness` — same interface we'd have used on the Touch Display 2. Write 0 for true off.

```go
// internal/display/backlight.go
package display

import (
    "fmt"
    "os"
)

const (
    // The exact path is determined by the SPI bus the HAT enumerates on.
    // Check after first boot: ls /sys/class/backlight/
    brightnessPath    = "/sys/class/backlight/spi0.0/brightness"
    maxBrightnessPath = "/sys/class/backlight/spi0.0/max_brightness"
)

type Backlight struct{ max int }

func NewBacklight() (*Backlight, error) {
    data, err := os.ReadFile(maxBrightnessPath)
    if err != nil { return nil, err }
    var max int
    fmt.Sscanf(string(data), "%d", &max)
    return &Backlight{max: max}, nil
}

// Set duty 0.0 (fully off, true zero) to 1.0 (full on).
func (b *Backlight) Set(duty float64) error {
    if duty < 0 { duty = 0 }
    if duty > 1 { duty = 1 }
    val := int(float64(b.max) * duty)
    return os.WriteFile(brightnessPath, []byte(fmt.Sprint(val)), 0644)
}
```

Grant the bedside user write access without root via udev. The configuration file is stored in the repository at [udev/90-backlight.rules](file:///Users/jasonadams/code/github/jasonkradams/bedside-clock/udev/90-backlight.rules).


### 4.5 HAT buttons + encoder in Go

All inputs are read identically through periph.io. The Display HAT Mini buttons are just GPIOs to ground — no library lock-in to Pimoroni's Python stack.

```go
// internal/input/handlers.go (abbreviated)
package input

import (
    "periph.io/x/conn/v3/gpio"
    "periph.io/x/conn/v3/gpio/gpioreg"
)

type Pins struct {
    BtnA, BtnB, BtnX, BtnY gpio.PinIO  // GPIO 5, 6, 16, 24
    EncA, EncB, EncSW      gpio.PinIO  // GPIO 17, 22, 23
}

func Open() (*Pins, error) {
    p := &Pins{
        BtnA:  gpioreg.ByName("GPIO5"),
        BtnB:  gpioreg.ByName("GPIO6"),
        BtnX:  gpioreg.ByName("GPIO16"),
        BtnY:  gpioreg.ByName("GPIO24"),
        EncA:  gpioreg.ByName("GPIO17"),
        EncB:  gpioreg.ByName("GPIO22"),
        EncSW: gpioreg.ByName("GPIO23"),
    }
    for _, pin := range []gpio.PinIO{
        p.BtnA, p.BtnB, p.BtnX, p.BtnY, p.EncA, p.EncB, p.EncSW,
    } {
        if err := pin.In(gpio.PullUp, gpio.BothEdges); err != nil {
            return nil, err
        }
    }
    return p, nil
}
```

---

## 5. Software setup

### 5.1 OS image

- Pi OS Lite (Bookworm, 64-bit). No desktop, no Xorg, no Wayland compositor.
- First boot: SSH enabled via `userconf.txt`, hostname `bedside`, fixed wifi creds via NetworkManager.
- **Read-only root + tmpfs overlay** for `/var/log` and Cog's WebKit cache. Use `overlayroot` (Debian) or hand-roll with `overlayfs` in initramfs. Position DB on a separate writable partition mounted noatime.
- **Optimized Kernel Command Line**: The optimized `/boot/firmware/cmdline.txt` configuration is stored in the repository at [boot/cmdline.txt](file:///Users/jasonadams/code/github/jasonkradams/bedside-clock/boot/cmdline.txt). It contains parameters (`quiet splash loglevel=3 logo.nologo`) to suppress system diagnostics and logos for a silent, appliance-like boot.
- **Boot Partition Cleanup**: To optimize disk space and remove clutter on a Pi Zero 2 W, delete redundant boot firmware and device trees for other architectures (e.g., `kernel_2712.img`, `initramfs_2712`, `start4*.elf`, `fixup4*.dat`, and unused `.dtb` files).
- **Automated Provisioning (cloud-init)**: The customized `/boot/firmware/user-data` configuration is stored in the repository at [boot/user-data](file:///Users/jasonadams/code/github/jasonkradams/bedside-clock/boot/user-data). It automates the installation of required packages, creates the `bedside` system user and groups, writes udev rules and systemd service files, and automatically starts services on first boot.


### 5.2 Partition layout

| Partition | FS    | Mount                          | Mode              | Purpose                                                     |
| --------- | ----- | ------------------------------ | ----------------- | ----------------------------------------------------------- |
| p1        | vfat  | /boot/firmware                 | ro at runtime     | Pi firmware + config.txt                                    |
| p2        | ext4  | / (lower)                      | ro via overlay    | OS, Go binary, Cog, assets                                  |
| p3        | ext4  | /var/lib/bedside               | rw, sync, noatime | boltdb (library, positions), cover cache, audiobook files   |
| tmpfs     | tmpfs | /var/log, /tmp, /var/cache/cog | rw                | Volatile; WebKit disk cache lives here so SD never sees it. |

### 5.3 Packages

```sh
# No xserver-xorg, no chromium. KMS/DRM-direct.
apt install --no-install-recommends \
  cog libwpebackend-fdo-1.0-1 \
  libwpewebkit-1.1-0 \
  mpv libmpv2 \
  fonts-inter \
  libasound2 alsa-utils \
  network-manager
```

**Verified May 2026**: Cog ships in Raspberry Pi OS Bookworm (cog 0.16.1-1, with libwpewebkit-1.1 dependencies). `apt install cog` works out of the box. If you need newer (worth doing — WebKit moves fast), pull from Igalia's repo or build from source.

**Caveat to track**: there are open reports of Cog/WPE WebKit lacking GPU acceleration on Pi 4 under Bookworm, falling back to software rasterization for some content. For our use case (mostly static HTML, occasional cover-art swap, no CSS animations) software raster is more than fine, but avoid CSS transitions and animated SVG. Use plain DOM updates via Datastar fragments — which is what we were doing anyway. Worth a smoke test on the actual device before committing.

Upstream: [github.com/Igalia/cog](https://github.com/Igalia/cog) · WebKitGTK: [webkitgtk.org](https://webkitgtk.org/) · Datastar: [data-star.dev](https://data-star.dev)

### 5.4 Boot ordering

systemd dependency graph for kiosk-up. No graphical.target, no display-manager:

```
network-online.target
        │
        v
bedside.service        (Go server: starts mpv, opens listeners on :8080)
        │
        v
bedside-ready.target   (custom: bedside.service has bound :8080)
        │
        v
cog.service            (Cog --platform=drm @ http://localhost:8080)
```

#### /etc/systemd/system/bedside.service

This service runs the Go server. The configuration file is stored in the repository at [systemd/bedside.service](file:///Users/jasonadams/code/github/jasonkradams/bedside-clock/systemd/bedside.service).

#### /etc/systemd/system/cog.service

This service runs the Cog kiosk on KMS/DRM. The configuration file is stored in the repository at [systemd/cog.service](file:///Users/jasonadams/code/github/jasonkradams/bedside-clock/systemd/cog.service).


**Why this is shorter than the Chromium version**: no Xorg, no Openbox, no unclutter, no PAM session, no display number, no user-data-dir tax. Cog opens `/dev/dri/card0` directly, KMS gives it the framebuffer, WebKitGTK draws into it. The user just needs `video` + `render` groups.

### 5.5 Time-keeping (NTP only)

The Pi has no real-time clock and we've explicitly skipped the DS3231 to keep the build cheap and the BOM short. Time-keeping relies on NTP:

- Pi OS Lite enables `systemd-timesyncd` by default; it picks up the configured NTP servers and corrects drift automatically once wifi is up.
- **Boot-up UX**: the clock displays `--:--` until time sync completes (usually <2s after wifi connects). The Go server gates the clock-display element on `!time.Now().Before(time.Unix(1700000000, 0))` — i.e., show dashes if the clock is implausibly old.
- **fake-hwclock** ships in Pi OS and writes the last-known time to disk on shutdown so boot starts roughly in the right decade. Leave it enabled.
- **If a power loss happens during a wifi outage**: clock reads dashes until wifi recovers. Acceptable trade-off for the $20 we saved by skipping the DS3231.
- **Adding a DS3231 later** is a 20-minute job: four-wire I2C breakout, one `dtoverlay=i2c-rtc,ds3231` line, `sudo apt remove fake-hwclock`. Don't pre-optimize.

### 5.6 Position resume strategy

Two layers:

- **On-device (bbolt)**: written every SSE tick (~1Hz) and again on `pause`/`stop`/`SIGTERM`. Survives power yank because bbolt fsyncs. This is the source of truth for cold boot.
- **mpv watch-later**: leave disabled — we own resume.

On boot: load last-played `fileHash` from bbolt, fetch metadata from boltdb `library` bucket, seek mpv to the chosen offset, do **not** auto-play. Show now-playing screen with a big play affordance — bedside ergonomics: never start blasting audio because power flickered.

### 5.7 Sleep timer as SSE-driven hypermedia

UI: rotary press on now-playing cycles `Off → 15 → 30 → 45 → 60 → Off`. Server holds a `time.Timer` and a `remaining` field. On every second, the Web component sends a Datastar `MergeFragments` patch to a `#sleep-timer` div. At T-30s, switch to fade mode: each tick lowers volume linearly via mpv `set_property volume X`. At T=0 emit `pause` and back to `Off`.

This is purely server state pushed as HTML — no client timer drift, no JS countdown logic, and the UI is exactly correct after a reconnect.

---

## 6. UI sketch with Datastar

### 6.1 Screens

| Screen          | Primary purpose                                             | Affordances                                                         |
| --------------- | ----------------------------------------------------------- | ------------------------------------------------------------------- |
| **Home**        | Glanceable status; clock when idle                          | Continue listening (1 card), library button, settings, current time |
| **Library**     | Browse + search                                             | Encoder scrolls list; press selects; back returns home              |
| **Now playing** | Most stateful screen; cover, chapter, progress, sleep timer | Play/pause, ±30s, sleep timer toggle, back                          |
| **Settings**    | Backlight, volume, scan library, restart                    | Encoder + buttons                                                   |
| **Clock-only**  | Display-off-but-clock mode                                  | Any button wakes to previous screen                                 |

### 6.2 Layout (Display HAT Mini, 320×240 landscape)

At 2.0" we have to be aggressive about hierarchy: clock is largest on idle, cover art shrinks to thumbnail on now-playing, transport hints map to the four onboard HAT buttons rather than touch targets. Library list shows 4 items at a time and scrolls via the rotary encoder.

```
Home / Idle                             Now Playing
┌──────────────────────────────┐        ┌──────────────────────────────┐
│                              │        │ Project Hail Mary    23:14   │
│           23:14              │        │ Ch.14 - "Eridian"            │
│                              │        │                              │
│      Project Hail Mary       │        │  ┌──────┐                    │
│      Ch.14  3h 12m left      │        │  │cover │  =====o--- 6:42    │
│                              │        │  │  90px│              /18:03│
│                              │        │  └──────┘                    │
│   [Library]   [Settings]     │        │                              │
│                              │        │  A:play  B:back  X:-30  Y:+30│
└──────────────────────────────┘        │  Sleep: 28:14                │
                                        └──────────────────────────────┘

Library browse                          Encoder maps:
┌──────────────────────────────┐        Rotate L/R --> scroll list / change volume
│ Library         (scrollable) │        Click       --> select / context action
│                              │
│ > Project Hail Mary  3h 12m  │        HAT button maps (default):
│   Cryptonomicon     12h 04m  │        A = Play/Pause
│   The Three-Body    8h 51m   │        B = Back / parent screen
│   Educated          11h 22m  │        X = Skip -30s
│                              │        Y = Skip +30s
└──────────────────────────────┘
```

### 6.3 Now-playing template (templ + Datastar)

Server endpoint `GET /now-playing` returns full HTML. `GET /sse` streams Datastar `merge-fragments` events as state changes.

```go
// internal/web/templates/nowplaying.templ
package templates

templ NowPlaying(s State) {
  <main id="screen"
        data-on-load="@get('/sse')"
        data-on-keydown__window="@post('/key', {key: evt.key})">
    <header>
      <button data-on-click="@post('/nav/back')">◀</button>
      <h1>Now Playing</h1>
    </header>

    <img id="cover" src={ "/cover/" + s.ItemID } width="480" height="480"/>

    <div id="meta">
      <h2 id="title">{ s.Title }</h2>
      <p id="chapter">{ s.ChapterTitle }</p>
    </div>

    <div id="progress" data-signals={ "{pos: " + fmtFloat(s.Position) + "}" }>
      <div id="bar" data-style-width="(signals.pos / { s.Duration }) * 100 + '%'"></div>
      <span id="elapsed" data-text="formatTime(signals.pos)">{ formatTime(s.Position) }</span>
      <span id="duration">{ formatTime(s.Duration) }</span>
    </div>

    <div id="controls">
      <button data-on-click="@post('/cmd/skip', {delta: -30})">−30</button>
      <button data-on-click="@post('/cmd/playpause')"
              data-text="signals.playing ? '⏸' : '⏯'">
        if s.Playing { ⏸ } else { ⏯ }
      </button>
      <button data-on-click="@post('/cmd/skip', {delta: 30})">+30</button>
    </div>

    <div id="sleep-timer">
      if s.SleepRemaining > 0 {
        💤 Sleep: { formatTime(s.SleepRemaining) }
      } else {
        <button data-on-click="@post('/sleep/cycle')">💤 Sleep timer</button>
      }
    </div>
  </main>
}
```

#### SSE handler emitting fragments

```go
// internal/web/sse.go
func (s *Server) sse(w http.ResponseWriter, r *http.Request) {
    sse := datastar.NewSSE(w, r)
    sub := s.bus.Subscribe(r.Context(),
        EventPlaybackTick, EventChapterChange,
        EventSleepTick, EventBacklight)

    for ev := range sub {
        switch e := ev.(type) {
        case PlaybackTick:
            // update only the bits that changed
            sse.MergeSignals([]byte(fmt.Sprintf(`{pos: %.2f, playing: %v}`,
                e.Position, e.Playing)))
        case ChapterChange:
            // re-render header + cover URL
            sse.MergeFragments(render(templates.NowPlayingHeader(e.State)))
        case SleepTick:
            sse.MergeFragments(render(templates.SleepTimerFragment(e.Remaining)))
        case Backlight:
            // fully off but clock-only? swap entire screen.
            if e.Mode == ClockOnly {
                sse.MergeFragments(render(templates.ClockOnly()))
            }
        }
    }
}
```

**Why this is clean**: position updates ride on `data-signals` (1 small JSON write per second, 80 bytes), but anything structural (chapter title, cover, sleep mode) is a server-rendered HTML fragment patched into the DOM. No client logic computes anything except `formatTime`, which is a one-line Datastar expression.

#### Input → command (rotary press cycles sleep timer)

```go
// internal/input/handlers.go — encoder push event
case InputEncoderPress:
    if s.screen == ScreenNowPlaying {
        s.bus.Publish(CmdSleepCycle{})
    }

// internal/web/handlers.go — also reachable via UI button
func (s *Server) sleepCycle(w http.ResponseWriter, r *http.Request) {
    s.bus.Publish(CmdSleepCycle{})
    w.WriteHeader(http.StatusNoContent)
}
```

### 6.4 Datastar attributes used

| Attribute                 | Where                                   | Effect                                                             |
| ------------------------- | --------------------------------------- | ------------------------------------------------------------------ |
| `data-on-load`            | Root `<main>`                           | Open SSE stream on page load.                                      |
| `data-on-click`           | Buttons                                 | POST to action endpoint; server responds 204 + emits SSE fragment. |
| `data-on-keydown__window` | Root                                    | Dev convenience: keyboard maps to GPIO actions on the desk.        |
| `data-signals`            | Progress block                          | Local signals updated by `merge-signals` events.                   |
| `data-text`               | Elapsed time, play/pause icon           | Bind text to a signal expression.                                  |
| `data-style-width`        | Progress bar                            | Reactive width as `pos/duration*100%`.                             |
| `data-show`               | Conditional UI (e.g., sleep timer pill) | Hide/show based on signal.                                         |

---

## 7. Bedside-specific gotchas

### 7.1 True-off backlight

Writing 0 to `/sys/class/backlight/.../brightness` on the Display HAT Mini fully extinguishes the LEDs — verify in a dark room before final assembly. If you see residual glow, add a P-channel MOSFET on the backlight power rail driven by a free GPIO and you have a hard kill.

### 7.2 Wake-on-button from display-off

Don't blank or kill Cog; that adds a flash on wake and forces a WebKit relayout. Instead leave Cog running, set backlight duty=0, and on `InputEvent` ramp duty back over 250ms. Encoder rotation should NOT wake (avoid accidental brushes); only **button presses** wake. The encoder push button counts as a button.

### 7.3 Clock-only mode

Distinct from display-off. Backlight at ~5%, screen renders a giant clock with no other elements (battery-AMOLED-style if you want), no cover. Triggered after N minutes of no playback + idle. Any button returns to last screen at full backlight.

### 7.4 Brownout / SD-card protection

- **Read-only root** as described in 5.2. The Pi will power-cycle ungracefully someday and you don't want fsck nor a corrupted WebKit cache.
- **Overlay tmpfs for `/var/log`** so journald doesn't write through to SD.
- **bbolt on rw partition** with `sync` mount option. Position writes are tiny; the SD endurance card handles them fine.
- **Watchdog**: enable BCM2837 hardware watchdog in `config.txt` (`dtparam=watchdog=on`) and `WatchdogSec=30` on `bedside.service`. If the Go server hangs, the hardware reboots.
- **Undervoltage flags**: monitor `vcgencmd get_throttled` in a tiny exporter to journald. Use a quality USB cable; bedside HDMI capture during testing eats current.

### 7.5 Fan-free thermal design

- Pi Zero 2 W idles at 45–55°C in a closed plastic case at 22°C ambient. Spoken-word playback is near-idle CPU load; Cog with one document and no animation hovers around 4–8% CPU — meaningfully lower than Chromium would have been.
- Display HAT Mini stacks directly on the GPIO header, sitting above the SoC. Idle thermals don't require a heatsink. If you ever load the device with anything heavier (don't), drill a few vent slots above the SoC in the enclosure.
- Hardwood final enclosure: cut a hidden vent slot at the back.

### 7.6 Boot UX

- Plymouth splash with a single static image (cover-art-style). No spinner — it looks like consumer firmware, not a Pi booting.
- `disable_overscan=1` even though we're SPI — sets a clean baseline for the framebuffer Cog inherits.
- Disable rainbow boot square in `config.txt`: `disable_splash=1`.

### 7.7 Audio gotchas

- MAX98357A has a small turn-on transient (click) when the amp first comes out of shutdown. Mitigate by leaving the SD pin floating (amp stays awake) and by keeping mpv running idle (`--idle`) so the I2S clock stays alive; sleep timer fades volume to silence rather than hard-pausing.
- ALSA volume limit: set a hard ceiling in `/etc/asound.conf` so a fat-fingered encoder spin can't blast at 3am. Map UI 0–100% to ALSA 0–80% of nominal.
- Sample-rate mismatch: Audiobook files may be 22.05 / 44.1 / 48 kHz mixed in a single book. Let mpv resample; don't try to set the ALSA device rate.
- **Speaker-driver sizing reality check**: CE32A-4 is a 1.25" full-range. It will not deliver chest-thumping bass — it doesn't need to. Spoken word lives in 200Hz–6kHz and the CE32A-4 covers that range cleanly. If you find yourself wanting more bottom-end in evaluation, a small (~30 cubic inch) sealed enclosure with a fistful of polyfill helps; do not chase the missing low end by EQ — you'll just bottom out the driver and clip.
- **Mono is fine for spoken word at this scale.** The MAX98357A is mono by design; if you ever want stereo, you wire two of them in parallel (one set to left channel, one to right via the SEL pin) and add a second CE32A-4. That's a $20 upgrade you can do later without ripping anything out.

### 7.8 Privacy / scope

- No microphone. No speech assistant. No phone pairing. The bedroom stays a phone-free zone — that's the whole point.
- Local network only. The device only connects to wifi for NTP time sync and Samba (SMB) file transfers. No outbound calls, no telemetry. Block egress at your router if you want belt-and-suspenders.

---

## 8. Build roadmap

| Phase | Milestone            | Definition of done                                                                                                                                    |
| ----- | -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| **0** | Bench rig            | Pi Zero 2 W + Display HAT Mini + MAX98357A breadboarded with a single CE32A-4 speaker. Plays a hardcoded local M4B; backlight goes to zero on demand. |
| **1** | Go skeleton          | templ + chi + Datastar SSE; Cog kiosk loads the page; pressing HAT Button A pauses playback.                                                          |
| **2** | Local Library Scan   | `ffprobe` scans `/var/lib/bedside/audiobooks`, populates `boltdb`, and mpv plays local files directly.                                                |
| **3** | Library UI           | Browse + search via encoder; cover art rendering at 480px square.                                                                                     |
| **4** | Now-playing complete | Chapter changes, ±30, sleep timer, real-time progress.                                                                                                |
| **5** | Bedside polish       | Backlight modes, clock-only, wake-on-button, read-only root, watchdog.                                                                                |
| **6** | Prototype enclosure  | PETG box; speaker baffle tuned; live with it for two weeks.                                                                                           |
| **7** | Final hardwood       | Once you've found what's wrong with the prototype.                                                                                                    |

_End of document._
