"""
Single source of truth for the Bedside Audiobook Appliance enclosure geometry.

Pure Python, NO Autodesk / adsk dependency, so it can be:
  * imported by validate_layout.py and run under plain python3 (hermetic tests), and
  * imported by fusion360_setup_v4.py inside Fusion 360 to drive the real geometry.

============================================================================
UNITS & COORDINATE SYSTEM
============================================================================
All numbers here are in MILLIMETRES. (The Fusion API works in cm, so the
builder divides by 10 on the way out. Keeping this file in mm keeps it
readable against the datasheets.)

Corner-origin, right-handed. Looking at the FRONT panel head-on:
    +X : to the viewer's right      (0 .. W, left wall .. right wall)
    +Y : up                          (0 .. H, bottom wall .. top wall)
    +Z : away from the viewer, INTO the case   (0 = front outer face, D = rear outer face)

So a component sitting just behind the screen has a small Z; the rear wall
inner face is at Z = D - wall.

============================================================================
VERIFIED HARDWARE DIMENSIONS  (primary sources cited)
============================================================================
Raspberry Pi Zero 2 W  -- official mechanical drawing
    datasheets.raspberrypi.com/rpizero2/raspberry-pi-zero-2-w-mechanical-drawing.pdf
    Board 65.0 x 30.0 mm, corner R3.0. 4x M2.5 holes on a 58 x 23 mm pattern,
    3.5 mm in from each edge. Bottom-edge connector centres (from left edge):
    mini-HDMI 12.4, micro-USB DATA 41.4, micro-USB PWR 54.0.

Pimoroni Display HAT Mini (PIM589) -- shop.pimoroni.com / core-electronics
    Board 65.5 x 35.0 x 9.0 mm (incl. header + display).
    2.0" 320x240 ST7789V2 IPS; usable/active area 40.8 x 30.6 mm.
    4 tactile buttons (A=GPIO5, B=GPIO6, X=GPIO16, Y=GPIO24) + RGB LED, one
    near each corner (A/B on the left, X/Y on the right).  << button XY is NOT
    published; see BUTTON_DX / BUTTON_DY below -- confirm with calipers. >>

Dayton Audio CE32A-4 -- daytonaudio.com/product/1138
    Overall outside 32 mm (square frame), overall depth 15.5 mm,
    baffle cutout 19 mm, voice coil 12.55 mm.

Adafruit MAX98357A (3006) -- adafruit.com/product/3006
    PCB 19.4 x 17.8 x 3.0 mm (3.0 = tallest fixed part). 2x M2.5 holes.
    Headers are user-soldered and REMOVABLE (wire directly) to reclaim depth.

Generic EC11 rotary encoder -- BOM
    12 x 12 mm body, ~M7 threaded bushing (7 mm), 6 mm D-shaft. 20 mm knob.
"""

from dataclasses import dataclass, field
from typing import List, Tuple, Optional, Dict

# ----------------------------------------------------------------------------
# Tunable parameters (mm).  These are what the validation loop iterates on.
# ----------------------------------------------------------------------------
PARAMS: Dict[str, float] = dict(
    # -- enclosure outer shell --
    W=108.0,          # width  (X)
    H=54.0,           # height (Y) -- driven by the 20mm knob above the 32mm speaker
    D=33.0,           # depth  (Z) -- fits the 8.5mm-max battery (datasheet) behind the stack
    wall=2.0,         # shell wall thickness
    clearance=1.5,    # minimum air gap required between distinct components

    # -- compute stack (Display HAT Mini + Pi Zero, joined by the 40-pin header) --
    stack_cx=36.0,    # X centre of the board stack (= screen centre X)
    stack_top=48.0,   # Y of the TOP (GPIO / header) edge of the stack
    gpio_gap=11.0,    # board-to-board spacing set by the 2x20 header (standard HAT)
    screen_front_gap=0.0,   # screen glass sits against the front window inner face

    # -- Display HAT Mini button centres, RELATIVE to the screen centre --
    # !!! ESTIMATE -- measure your HAT's 4 button centres and set these two. !!!
    # Plungers & cutouts are derived from these, so plunger==button always holds.
    BUTTON_DX=24.0,   # |X| offset of each button from screen centre (ESTIMATE: clears the corner standoffs)
    BUTTON_DY=8.0,    # |Y| offset of each button from screen centre (ESTIMATE: measure & confirm)

    # -- right-hand controls column (encoder above, speaker below) --
    right_cx=88.0,    # X centre of the encoder + speaker column
    enc_cy=42.0,      # Y centre of the rotary encoder (front-panel mounted)
    spk_cy=18.5,      # Y centre of the speaker
    grille_radius=11.0,  # radius of the front dot-grille field (over the ~19mm cone)

    # -- audio amp (MAX98357A), rear wall, upper-right (battery now takes the Pi shadow) --
    amp_cx=82.0,      # X centre
    amp_cy=41.5,      # Y centre (above the charger, clear of it)
    amp_headers=False,  # True = keep soldered headers (needs more depth); False = wired

    # -- power subsystem (Adafruit 6106 all-in-one: USB-C charge + 5V boost + power-path) --
    battery_cx=35.0,  # PKCELL LP803860 2000mAh, MAX 62x38.5x8.5 (datasheet), flat on rear wall
    battery_cy=26.0,
    charger_cx=88.0,  # 6106 ~34x27x8, rear wall lower-right, USB-C out the right side
    charger_cy=16.5,
)

# ----------------------------------------------------------------------------
# Geometry primitives
# ----------------------------------------------------------------------------
@dataclass
class Part:
    """One axis-aligned solid belonging to a component.

    kind='box'  -> uses dx,dy,dz (full sizes, centred on cx,cy,cz)
    kind='cyl'  -> uses radius, length, axis ('x'|'y'|'z'); AABB is conservative
    role: 'body' (must stay inside), or 'protrude:<opening>' (may exit via opening)
    """
    name: str
    kind: str
    cx: float
    cy: float
    cz: float
    dx: float = 0.0
    dy: float = 0.0
    dz: float = 0.0
    radius: float = 0.0
    length: float = 0.0
    axis: str = "z"
    role: str = "body"

    def aabb(self) -> Tuple[float, float, float, float, float, float]:
        if self.kind == "box":
            hx, hy, hz = self.dx / 2, self.dy / 2, self.dz / 2
        else:  # cylinder -> bounding box
            r, L = self.radius, self.length / 2
            if self.axis == "x":
                hx, hy, hz = L, r, r
            elif self.axis == "y":
                hx, hy, hz = r, L, r
            else:
                hx, hy, hz = r, r, L
        return (self.cx - hx, self.cx + hx,
                self.cy - hy, self.cy + hy,
                self.cz - hz, self.cz + hz)


@dataclass
class Component:
    name: str
    group: str
    parts: List[Part] = field(default_factory=list)
    allow_contact: Tuple[str, ...] = ()   # groups this one may touch without penalty
    note: str = ""

    def aabb(self):
        boxes = [p.aabb() for p in self.parts]
        return (min(b[0] for b in boxes), max(b[1] for b in boxes),
                min(b[2] for b in boxes), max(b[3] for b in boxes),
                min(b[4] for b in boxes), max(b[5] for b in boxes))


@dataclass
class Opening:
    """A hole in a wall that a protruding part is allowed to pass through."""
    name: str
    wall: str          # 'front','rear','bottom','top','left','right'
    cx: float
    cy: float
    cz: float
    w: float           # size in the two in-plane axes
    h: float


@dataclass
class Layout:
    p: Dict[str, float]
    components: List[Component] = field(default_factory=list)
    openings: List[Opening] = field(default_factory=list)
    buttons: List[Tuple[str, float, float]] = field(default_factory=list)   # (label, x, y)
    screen_center: Tuple[float, float] = (0.0, 0.0)
    usb_center: Tuple[float, float, float] = (0.0, 0.0, 0.0)

    # --- enclosure helpers ---
    @property
    def W(self): return self.p["W"]
    @property
    def H(self): return self.p["H"]
    @property
    def D(self): return self.p["D"]
    @property
    def wall(self): return self.p["wall"]

    def interior(self):
        w = self.wall
        return (w, self.W - w, w, self.H - w, w, self.D - w)


# ----------------------------------------------------------------------------
# Build the placed layout from parameters
# ----------------------------------------------------------------------------
def compute(p: Optional[Dict[str, float]] = None) -> Layout:
    if p is None:
        p = PARAMS
    W, H, D, wall = p["W"], p["H"], p["D"], p["wall"]
    lay = Layout(p=dict(p))

    # ===== Z-stack of the compute assembly (front -> back) =====
    z_screen_front = wall + p["screen_front_gap"]      # front face of the glass
    screen_dz = 2.0
    z_hat = z_screen_front + screen_dz                 # front face of HAT PCB
    hat_dz = 1.6
    z_gpio = z_hat + hat_dz                            # front face of GPIO gap
    z_pi = z_gpio + p["gpio_gap"]                      # front face of Pi PCB
    pi_dz = 1.4
    z_pi_back = z_pi + pi_dz

    stack_cx, stack_top = p["stack_cx"], p["stack_top"]
    hat_cy = stack_top - 35.0 / 2.0                    # HAT is 35 tall, top edge at stack_top
    pi_cy = stack_top - 30.0 / 2.0                     # Pi is 30 tall, shares top (header) edge
    screen_cx, screen_cy = stack_cx, hat_cy            # screen centred on HAT
    lay.screen_center = (screen_cx, screen_cy)

    stack = Component("Compute_Stack", "stack",
                      allow_contact=("wires",),
                      note="Display HAT Mini + Pi Zero 2 W, joined by the 40-pin header")
    # screen active area (also the front-window reference); may show through front
    stack.parts.append(Part("Screen_active", "box", screen_cx, screen_cy,
                            z_screen_front + screen_dz / 2, 40.8, 30.6, screen_dz,
                            role="protrude:front_window"))
    # HAT PCB
    stack.parts.append(Part("HAT_pcb", "box", stack_cx, hat_cy, z_hat + hat_dz / 2,
                            65.5, 35.0, hat_dz))
    # GPIO header block (near the top edge, spans the pin field)
    stack.parts.append(Part("GPIO_header", "box", stack_cx, stack_top - 3.5,
                            z_gpio + p["gpio_gap"] / 2, 51.0, 5.0, p["gpio_gap"]))
    # Pi PCB
    stack.parts.append(Part("Pi_pcb", "box", stack_cx, pi_cy, z_pi + pi_dz / 2,
                            65.0, 30.0, pi_dz))
    # Pi back keep-out (male header tails, connectors' bodies)
    stack.parts.append(Part("Pi_back", "box", stack_cx, pi_cy, z_pi_back + 1.0,
                            60.0, 26.0, 2.0))
    # Pi micro-USB PWR receptacle: on the bottom edge (Y-), at X = left_edge+54
    pi_left = stack_cx - 65.0 / 2.0
    usb_x = pi_left + 54.0
    pi_bottom = pi_cy - 30.0 / 2.0
    usb_z = z_pi + pi_dz / 2.0                          # connector straddles the board plane
    # receptacle body protrudes ~2 mm below the board edge (toward the bottom wall)
    stack.parts.append(Part("Pi_USB_pwr", "box", usb_x, pi_bottom - 1.0, usb_z,
                            8.0, 4.0, 3.0))   # internal/unused: Pi now powered via 5V boost -> pads
    lay.usb_center = (usb_x, pi_bottom, usb_z)
    lay.components.append(stack)

    # ===== Display HAT Mini buttons + derived plungers =====
    bdx, bdy = p["BUTTON_DX"], p["BUTTON_DY"]
    lay.buttons = [
        ("A", screen_cx - bdx, screen_cy + bdy),   # top-left
        ("B", screen_cx - bdx, screen_cy - bdy),   # bottom-left
        ("X", screen_cx + bdx, screen_cy + bdy),   # top-right
        ("Y", screen_cx + bdx, screen_cy - bdy),   # bottom-right
    ]

    # ===== Speaker (front-firing, right column, below the encoder) =====
    right_cx = p["right_cx"]
    spk_cy = p["spk_cy"]
    spk_depth = 15.5
    z_spk_front = wall                                 # frame front sits at the inner wall
    spk = Component("Speaker_CE32A_4", "speaker",
                    note="Dayton CE32A-4, 32x32 frame, 15.5 deep, fires through grille")
    # square frame
    spk.parts.append(Part("Spk_frame", "box", right_cx, spk_cy, z_spk_front + 2.0 / 2,
                          32.0, 32.0, 2.0))
    # cone + magnet body behind the frame (approx by a cylinder)
    spk.parts.append(Part("Spk_body", "cyl", right_cx, spk_cy, z_spk_front + spk_depth / 2,
                          radius=28.0 / 2, length=spk_depth, axis="z"))
    lay.components.append(spk)

    # ===== Rotary encoder (front-panel mounted, above the speaker) =====
    enc_cy = p["enc_cy"]
    enc = Component("Rotary_Encoder_EC11", "encoder",
                    allow_contact=("wires",),
                    note="EC11, 12x12 body, M7 neck, 6mm D-shaft, 20mm knob")
    # body behind the panel
    enc_body_dz = 6.5
    enc.parts.append(Part("Enc_body", "box", right_cx, enc_cy, wall + enc_body_dz / 2,
                          12.0, 12.0, enc_body_dz))
    # pins sticking further back
    enc.parts.append(Part("Enc_pins", "box", right_cx, enc_cy, wall + enc_body_dz + 3.0 / 2,
                          12.0, 8.0, 3.0))
    # threaded neck through the panel (protrudes out the front)
    enc.parts.append(Part("Enc_neck", "cyl", right_cx, enc_cy, wall / 2,
                          radius=7.0 / 2, length=wall + 4.0, axis="z",
                          role="protrude:enc_hole"))
    # shaft + knob out front
    enc.parts.append(Part("Enc_knob", "cyl", right_cx, enc_cy, -8.0,
                          radius=20.0 / 2, length=16.0, axis="z",
                          role="protrude:enc_hole"))
    lay.components.append(enc)

    # ===== Audio amp MAX98357A (rear inner wall) =====
    amp_cx, amp_cy = p["amp_cx"], p["amp_cy"]
    # Adafruit's 3.0 mm is the TOTAL thickness; component side is ~1.4 mm above the
    # 1.6 mm PCB. With headers kept, the terminal block/pins add ~8.5 mm.
    amp_keepout = 8.5 if p["amp_headers"] else 1.5     # headers removable per BOM note
    amp = Component("Audio_Amp_MAX98357A", "amp",
                    allow_contact=("wires",),
                    note="Adafruit 3006; component side faces into case")
    z_amp_back = D - wall
    amp.parts.append(Part("Amp_pcb", "box", amp_cx, amp_cy, z_amp_back - 1.6 / 2,
                          19.4, 17.8, 1.6))
    amp.parts.append(Part("Amp_parts", "box", amp_cx, amp_cy, z_amp_back - 1.6 - amp_keepout / 2,
                          19.4, 17.8, amp_keepout))
    lay.components.append(amp)

    # ===== Power subsystem (v5): battery + charger + boost + fuel gauge =====
    z_rear = D - wall                                    # rear inner wall face
    # 2000mAh LiPo (60 x 36 x 7), flat against the rear wall behind the compute stack
    bat = Component("Battery_2000mAh", "battery",
                    note="PKCELL LP803860 (Adafruit 2011); MAX 62 x 38.5 x 8.5 per datasheet; snug pocket")
    bat.parts.append(Part("Bat_cell", "box", p["battery_cx"], p["battery_cy"], z_rear - 8.5 / 2,
                          62.0, 38.5, 8.5))
    lay.components.append(bat)
    lay.battery_center = (p["battery_cx"], p["battery_cy"], z_rear - 8.5 / 2)

    # Adafruit 6106: bq25185 USB-C charger + TPS61023 5V boost + power-path, ONE board
    # (~34 x 27 x 8). Rear wall lower-right; USB-C exits the RIGHT wall. No separate
    # boost or fuel gauge -- battery status is read coarsely from the charger pins.
    chg = Component("Charger_6106", "charger", allow_contact=("wires",),
                    note="Adafruit 6106 all-in-one (USB-C charge + 5V boost); USB-C exits right wall")
    chg.parts.append(Part("Chg_pcb", "box", p["charger_cx"], p["charger_cy"], z_rear - 1.6 / 2,
                          34.0, 27.0, 1.6))
    chg.parts.append(Part("Chg_parts", "box", p["charger_cx"], p["charger_cy"], z_rear - 1.6 - 6.4 / 2,
                          34.0, 27.0, 6.4))
    chg_right = p["charger_cx"] + 34.0 / 2.0
    usbc_z = z_rear - 4.0
    # connector sits just BEYOND the board's right edge (no overlap, so it stays one body)
    chg.parts.append(Part("Chg_usbc", "box", chg_right + 2.2, p["charger_cy"], usbc_z,
                          4.0, 9.0, 3.3, role="protrude:usbc"))
    lay.components.append(chg)
    lay.usbc_center = (chg_right, p["charger_cy"], usbc_z)

    # ===== Openings (holes a protruding part may legally pass through) =====
    lay.openings.append(Opening("front_window", "front", screen_cx, screen_cy, 0.0,
                                40.8, 30.6))
    lay.openings.append(Opening("enc_hole", "front", right_cx, enc_cy, 0.0, 7.4, 7.4))
    lay.openings.append(Opening("usbc", "right", chg_right, p["charger_cy"], usbc_z, 9.5, 4.0))
    return lay


if __name__ == "__main__":
    lay = compute()
    print("Layout computed OK.")
    print("  screen centre:", lay.screen_center, "mm")
    print("  usb port centre:", lay.usb_center, "mm")
    print("  components:", [c.name for c in lay.components])
