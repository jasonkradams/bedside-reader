# Bedside Audiobook Appliance — Bill of Materials

Tier 2 lean build. Pi Zero 2 W + Display HAT Mini + MAX98357A + mono CE32A-4.
Prices delivered to ZIP 99037 (Spokane Valley, WA). Verify in cart — retailer prices and stock fluctuate.

## Compute

| Item | Part | Supplier | Qty | Delivered |
|---|---|---|---|---|
| SBC | [Raspberry Pi Zero 2 WH (pre-soldered headers)](https://www.pishop.us/product/raspberry-pi-zero-2-wh/) | PiShop.us | 1 | ~$24 |
| microSD | [SanDisk High Endurance 32GB](https://www.amazon.com/SanDisk-Endurance-microSDXC-Adapter-Monitoring/dp/B07P3D6Y5B) | Amazon | 1 | ~$10 |
| PSU | [CanaKit 2.5A micro-USB (UL listed)](https://www.amazon.com/CanaKit-Raspberry-Supply-Adapter-Listed/dp/B00MARDJZ4) | Amazon | 1 | ~$10 |
| Case | [Generic Pi Zero 2 W plastic case](https://www.amazon.com/s?k=raspberry+pi+zero+2+w+case) | Amazon | 1 | ~$6 |

## Display

| Item | Part | Supplier | Qty | Delivered |
|---|---|---|---|---|
| Display + buttons + LED | [Pimoroni Display HAT Mini (PIM589)](https://www.pishop.us/product/display-hat-mini/) | PiShop.us | 1 | ~$30 |

2.0" 320×240 IPS SPI. Four onboard tactile buttons (A/B/X/Y on GPIO 5/6/16/24) + RGB LED — replaces a separate button board entirely.

## Audio

| Item | Part | Supplier | Qty | Delivered |
|---|---|---|---|---|
| I2S amp breakout | [Adafruit MAX98357A I2S Class-D Mono Amp](https://www.adafruit.com/product/3006) | Adafruit | 1 | ~$13 |
| Speaker driver | [Dayton Audio CE32A-4 1.25" Mini Speaker, 4Ω](https://www.parts-express.com/Dayton-Audio-CE32A-4-1-1-4-Mini-Speaker-4-Ohm-285-103) | Parts Express | 1 | ~$15 |

Mono. Add a second MAX98357A + CE32A-4 later if you want stereo (~$20 upgrade, no rework).

## Controls

| Item | Part | Supplier | Qty | Delivered |
|---|---|---|---|---|
| Rotary encoder + push | [Generic EC11 24-detent encoder w/ pushbutton (5-pack)](https://www.amazon.com/s?k=ec11+rotary+encoder+with+push+button) | Amazon | 1 | ~$8 |
| Encoder knob | [Aluminum 20mm knob, 6mm shaft](https://www.amazon.com/s?k=aluminum+knob+20mm+6mm) | Amazon | 1 | ~$5 |

HAT buttons cover Play/Pause, Back, Skip ±30. Encoder handles scroll + volume.

## Power / Battery

| Item | Part | Supplier | Qty | Delivered |
|---|---|---|---|---|
| USB-C charger + 5V boost (all-in-one) | [Adafruit bq25185 USB-C Charger **with 5V Boost** (6106)](https://www.adafruit.com/product/6106) | Adafruit | 1 | ~$10 |
| Battery | [Lithium Ion Polymer 3.7V 2000mAh (2011)](https://www.adafruit.com/product/2011) | Adafruit | 1 | ~$13 |
| JST-PH pigtails | 2-pin JST-PH cables (battery ↔ charger) | Amazon | 1 | ~$4 |

Runtime: 2000mAh ÷ ~350mA average ≈ **5–6 h** of playback; charges while plugged in.
Cell is a PKCELL **LP803860**, **max 62 × 38.5 × 8.5 mm** per its [datasheet](datasheets/LiIon2000mAh37V.pdf) (bigger than Adafruit's rounded "60×36×7" — the case pocket is sized to the datasheet max).

**Single-board power chain: USB-C → 6106 (charge + 5V boost + power-path) → Pi.** The
6106 folds the charger, the 5V boost (TPS61023) and load-sharing onto one board, so
there's no separate boost or fuel gauge — the whole power subsystem is **one board +
battery**.
- USB-C into the **6106** charges the **2000mAh LiPo** (2011, JST-PH) and outputs a
  regulated **5V** on its LOAD connector, power-path switching between USB and battery
  — continuous charge **and** load.
- ⚠️ **The 6106 boost is 1A max** and dislikes big instant loads at power-up. The Pi
  Zero 2 W's boot inrush + wifi/audio peaks can flirt with that ceiling, so add a
  **bulk cap (470–1000µF) across the 5V LOAD** to swallow inrush. Fine for typical
  audiobook use; if you want real headroom, the AmpRipper 3000 (USB-C, 3A) is the
  step-up option.

**Battery indicator (coarse — no fuel gauge on this board).** Read the 6106's charge
status pins on GPIO for a "plugged / charging / charged" icon next to the wifi
symbol. There's no state-of-charge **%** without a gauge; to add one later, drop in a
**MAX17048** (#5580) on I²C1 (GPIO2/3, 0x36) — the bus is free.

**Battery-subsystem GPIO** (all free — the HAT uses SPI for the display, GPIO0/1 for its ID EEPROM):

| Signal | BCM | Header pin | Notes |
|---|---|:---:|---|
| Charger PG (power-good) | GPIO4 | 7 | USB present → "plugged / charging" |
| Charger CHG (charge status) | GPIO26 | 37 | charging vs charge-complete |
| *(future)* gauge SDA/SCL | GPIO2/3 | 3/5 | I²C1 free if you add a MAX17048 for % |

**Pi power connection:** solder the 6106's **5V LOAD → Pi pad PP1** and **GND → PP3**
(underside test pads) — the boost is already on the board, so no in-line converter.
The Pi's own micro-USB stays unused/internal. Soldering (vs a micro-USB pigtail)
saves the ~15mm of depth a connector would eat and matches the rotary/amp wires
already tacked to the Pi's back.

## Wiring & enclosure

| Item | Part | Supplier | Qty | Delivered |
|---|---|---|---|---|
| Jumpers | [Dupont jumper kit, M-F/M-M/F-F (120 pcs)](https://www.amazon.com/s?k=dupont+jumper+wire+kit) | Amazon | 1 | ~$7 |
| Enclosure | PETG print (your design) or repurposed box | self-print / junk drawer | 1 | ~$0–10 |

## Order plan

Three orders cover the whole build:

- **PiShop.us**: Pi Zero 2 WH + Display HAT Mini → ~$54 + $7 USPS = **~$61**
- **Adafruit**: MAX98357A → ~$6 + $7 USPS = **~$13**
- **Amazon Prime**: SD card, PSU, case, encoder, knob, Dupont jumpers → **~$36**
- **Parts Express**: CE32A-4 ($5) + $9.95 flat ground = **~$15** (skip if you can scrounge a speaker from a junk drawer)

## Cost summary

| Line | Cost |
|---|---|
| Compute (Pi + SD + PSU + case) | ~$50 |
| Display (Display HAT Mini) | ~$30 |
| Audio (MAX98357A + CE32A-4) | ~$28 |
| Controls (encoder + knob) | ~$13 |
| Power (USB-C charger+boost all-in-one + 2000mAh battery) | ~$27 |
| Wiring + enclosure | ~$15 |
| **Total delivered (estimated)** | **~$162** |

Unique-to-this-build parts (if you already own PSU, SD, jumpers, case): **~$77**

## Enclosure Hardware

| Item | Fastener / Hardware | Quantity | Notes |
|---|---|:---:|---|
| Main Enclosure (Corners) | M3 x 50mm Screw | 4 | Secures the front and rear halves together. (Socket head or pan head). |
| Display HAT Mount | M2.5 x 6mm Screw | 4 | Mounts the HAT to the front faceplate standoffs. |
| Audio Amp Mount | M2.5 x 6mm Screw | 2 | Mounts the amp to the floor standoffs in the rear bucket. |
| Speaker Mount | M2.5 x 6mm Screw | 4 | Mounts the CE32A-4 driver to the internal acoustic chamber. |
| Pi Zero Support (Optional) | M2.5 Brass Standoffs | 4 | Optional spacer between Pi and HAT for extreme rigidity. |
| Rotary Encoder | Hex Nut & Washer | 1 | Included with the encoder; secures it to the top panel hole. |
| Speaker Lid | Superglue / VHB Tape | 1 | Secures the acoustic chamber lid once the speaker is wired. |
