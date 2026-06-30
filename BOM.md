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
| Wiring + enclosure | ~$15 |
| **Total delivered (estimated)** | **~$135** |

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
