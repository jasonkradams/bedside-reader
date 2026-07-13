# Bedside Audiobook Appliance Enclosure Design Specification

This document outlines the basic physical structure, layout constraints, and visual direction for the Bedside Audiobook Appliance chassis.

> **Authoritative geometry lives in code (v4).** The exact, datasheet-checked
> dimensions and positions are in [`hardware/fusion360/layout_spec.py`](../fusion360/layout_spec.py),
> proven by [`validate_layout.py`](../fusion360/validate_layout.py), and built by
> [`fusion360_setup_v4.py`](../fusion360/fusion360_setup_v4.py). If this prose and
> those files disagree, the files win. Superseded during v4: the Display HAT Mini
> and Pi Zero are now joined **directly by the 40-pin GPIO header** (stacked, not a
> wire harness). **Power (v5): the device is now battery-powered** — a single
> all-in-one USB-C charger + 5V boost (Adafruit **#6106**) charges an internal
> 2000 mAh LiPo (#2011) and feeds the Pi 5V directly (no separate boost/gauge). Its
> USB-C exits the **right wall**; the Pi's own micro-USB is **internal/unused** and
> its old bottom cutout is removed. See [BOM → Power / Battery](../BOM.md#power--battery)
> for the chain and GPIO map. Shell is **108 × 54 × 33 mm** to hold the battery
> (PKCELL LP803860, **max 62 × 38.5 × 8.5 mm** per its
> [datasheet](../datasheets/LiIon2000mAh37V.pdf)).

## 1. Component Constraints & Tolerances
To properly model the enclosure, we must account for the following component footprints:
*   **Display HAT Mini (Screen & Buttons)**:
    *   *Footprint*: ~65.5mm (W) x 35mm (H) x ~7mm (D).
    *   *Mounting*: Mounted flush against the inside of the front bezel using custom standoffs or retaining clips. Connected to the Pi via soldered wire harness.
*   **Raspberry Pi Zero 2 W (Compute)**:
    *   *Footprint*: 65mm (W) x 30mm (H) x ~5mm (D).
    *   *Mounting*: 4x M2.5 standoffs mounted directly to the enclosure floor.
*   **Audio Amp (MAX98357A)**: 
    *   *Footprint*: ~19.4mm x 17.8mm x 3.0mm.
    *   *Mounting*: Can be mounted on interior standoffs or slid into a printed rail/slot away from the main stack.
*   **Speaker (Dayton CE32A-4)**: 
    *   *Footprint*: 32mm x 32mm square frame, 15.5mm depth.
    *   *Mounting*: Front-firing through a grille on the right side of the faceplate.
*   **Controls**: 
    *   *Tactile Buttons (x4)*: Embedded directly on the Display HAT at the four corners of the screen. We will use 3D-printed flexible "plungers" built into the front faceplate to actuate them from the outside.
    *   *Rotary Encoder (EC11)*: ~12mm x 12mm body with a threaded neck and D-shaft. Needs a dedicated 7mm mounting hole on the right side of the front panel.
*   **Power Jack (Panel-Mount Micro-USB)**:
    *   *Mounting*: Fastens directly to the rear panel. Screw hole pitch is exactly 18mm (0.7 inch).

## 2. Structural Decisions
*   **Orientation**: Wide, horizontal desktop aspect ratio.
*   **Top/Side Surfaces**: Completely flat and clean (no snooze button, no side cables).
*   **Faceplate Left**: 2.0" landscape display showing audiobook playback UI. Four plunger buttons located precisely at the NW, NE, SW, and SE corners of the screen.
*   **Faceplate Right**: Prominent rotary encoder knob and dotted speaker grille.
*   **Power Access**: Micro-USB port cutout directly on the rear panel. 
*   **Assembly**: A two-piece shell. A front bezel/chassis that holds the components, and a rear "bucket" or hood that snaps or screws into place.

## 3. Button Mapping (Audiobook Reader)
The four corner buttons around the screen will map to the following audiobook controls:
*   **NW (Top-Left)**: Fast Forward (FF)
*   **SW (Bottom-Left)**: Rewind (RW)
*   **NE (Top-Right)**: Menu
*   **SE (Bottom-Right)**: Play/Pause

## 4. Final Design Mockup

This mockup reflects the front-firing speaker choice, the completely clean top and sides, corner screen buttons, and an audiobook interface on the display. The power cable exits entirely from the rear.

![Final Front Speaker Mockup](./mockup.png)

## 5. Front Panel Layout

Please see [front_panel_layout.svg](./front_panel_layout.svg) for the exact CAD dimension layout mapping screen, button, and knob centerlines.

![Front Panel Layout Schematic](./front_panel_layout.svg)

## 6. Internal Layout (X-Ray Footprints)

To aid in arranging the internal standoffs and rails, refer to these internal cross-sections showing the 3D stacking of the components within the chassis:

*   **Top-Down Slice (X/Z Plane)**: Shows horizontal layout and depth clearance.  
    ![Top-Down Slice](./internal_layout_top_down.svg)
*   **Side-Profile Slice (Y/Z Plane)**: Shows vertical stacking of encoder/speaker.  
    ![Side-Profile Slice](./internal_layout_side_profile.svg)

## 7. 3D Modeling Guide

To start modeling this chassis in Fusion 360 using parameters, see the [Fusion 360 Enclosure Design Guide](../fusion360/fusion_360_enclosure_guide.md).

