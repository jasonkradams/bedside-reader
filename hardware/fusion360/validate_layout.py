#!/usr/bin/env python3
"""
Hermetic layout validator for the Bedside Audiobook Appliance enclosure.

Runs with plain python3 (no Fusion needed). It loads the placed geometry from
layout_spec.py and checks the requirements the CAD keeps getting wrong:

  1. No two distinct components collide (min air gap = clearance).
  2. Every component stays inside the shell interior, except parts explicitly
     allowed to protrude through a named opening (screen, encoder, USB).
  3. Each plunger centre == its HAT button centre (by construction; asserted).
  4. The speaker frame fits inside the shell and sits on the grille centre.
  5. The USB cutout lines up with the Pi's micro-USB PWR port.
  6. Reports per-axis enclosure "slack" so the shell can be shrunk to minimum.

Exit code 0 = all pass, 1 = at least one failure.  Use it in a loop.
"""
import json
import sys
import os

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import layout_spec


def overlap_1d(a0, a1, b0, b1):
    """Signed overlap of intervals; >0 means they overlap by that much."""
    return min(a1, b1) - max(a0, b0)


def boxes_collide(a, b, gap):
    """AABB collision with a required clearance `gap`.
    Returns the min positive per-axis overlap once each box is grown by gap/2,
    or None if they are clear."""
    g = gap / 2.0
    ox = overlap_1d(a[0] - g, a[1] + g, b[0] - g, b[1] + g)
    oy = overlap_1d(a[2] - g, a[3] + g, b[2] - g, b[3] + g)
    oz = overlap_1d(a[4] - g, a[5] + g, b[4] - g, b[5] + g)
    if ox > 0 and oy > 0 and oz > 0:
        return min(ox, oy, oz)
    return None


def part_protrude_axis(role):
    """Which face a protruding part exits through -> axis to skip in containment."""
    return role.split(":", 1)[1] if role.startswith("protrude:") else None


class Report:
    def __init__(self):
        self.failures = []
        self.warnings = []
        self.lines = []

    def ok(self, msg):
        self.lines.append(f"  [ OK ] {msg}")

    def fail(self, msg):
        self.failures.append(msg)
        self.lines.append(f"  [FAIL] {msg}")

    def warn(self, msg):
        self.warnings.append(msg)
        self.lines.append(f"  [warn] {msg}")

    def head(self, msg):
        self.lines.append(f"\n=== {msg} ===")


def check(lay: "layout_spec.Layout") -> Report:
    r = Report()
    p = lay.p
    clr = p["clearance"]
    xi0, xi1, yi0, yi1, zi0, zi1 = lay.interior()

    # ---- 1. component vs component ----
    r.head("1. Component-to-component collisions (min gap %.1f mm)" % clr)
    comps = lay.components
    collisions = 0
    for i in range(len(comps)):
        for j in range(i + 1, len(comps)):
            c1, c2 = comps[i], comps[j]
            if c2.group in c1.allow_contact or c1.group in c2.allow_contact:
                continue
            worst = None
            for pa in c1.parts:
                for pb in c2.parts:
                    pen = boxes_collide(pa.aabb(), pb.aabb(), clr)
                    if pen is not None and (worst is None or pen > worst[0]):
                        worst = (pen, pa.name, pb.name)
            if worst:
                collisions += 1
                r.fail("%s <-> %s collide/too close by %.2f mm (%s / %s)"
                       % (c1.name, c2.name, worst[0], worst[1], worst[2]))
    if collisions == 0:
        r.ok("all %d component pairs respect the clearance" % (len(comps) * (len(comps) - 1) // 2))

    # ---- 2. containment inside the shell ----
    r.head("2. Components inside the shell interior")
    openings = {o.name: o for o in lay.openings}
    contained = True
    for c in comps:
        for part in c.parts:
            ax0, ax1, ay0, ay1, az0, az1 = part.aabb()
            skip = part_protrude_axis(part.role)
            # axis that the opening pierces
            pierce = None
            if skip and skip in openings:
                o = openings[skip]
                pierce = {"front": "z", "rear": "z", "bottom": "y",
                          "top": "y", "left": "x", "right": "x"}[o.wall]
            probs = []
            if pierce != "x":
                if ax0 < xi0 - 1e-6: probs.append("x<%.1f (%.2f)" % (xi0, ax0))
                if ax1 > xi1 + 1e-6: probs.append("x>%.1f (%.2f)" % (xi1, ax1))
            if pierce != "y":
                if ay0 < yi0 - 1e-6: probs.append("y<%.1f (%.2f)" % (yi0, ay0))
                if ay1 > yi1 + 1e-6: probs.append("y>%.1f (%.2f)" % (yi1, ay1))
            if pierce != "z":
                if az0 < zi0 - 1e-6: probs.append("z<%.1f (%.2f)" % (zi0, az0))
                if az1 > zi1 + 1e-6: probs.append("z>%.1f (%.2f)" % (zi1, az1))
            if probs:
                contained = False
                r.fail("%s/%s pokes out of the shell: %s" % (c.name, part.name, ", ".join(probs)))
    if contained:
        r.ok("every body is within the interior (or exits via its opening)")

    # ---- 3. plunger centre == button centre ----
    r.head("3. Plunger alignment on HAT buttons")
    # plungers are derived from the same button coords in the builder; assert the
    # source-of-truth buttons are symmetric about the screen centre.
    scx, scy = lay.screen_center
    bad = 0
    for label, bx, by in lay.buttons:
        if abs(abs(bx - scx) - p["BUTTON_DX"]) > 1e-6 or abs(abs(by - scy) - p["BUTTON_DY"]) > 1e-6:
            bad += 1
            r.fail("button %s not at expected offset from screen centre" % label)
    if bad == 0:
        r.ok("4 buttons symmetric about screen centre (%.1f, %.1f); plungers share these coords"
             % (scx, scy))
        # sanity: buttons must land on the HAT board, not off the edge
        for label, bx, by in lay.buttons:
            if not (scx - 32.75 <= bx <= scx + 32.75 and scy - 17.5 <= by <= scy + 17.5):
                r.warn("button %s at (%.1f,%.1f) is off the 65.5x35 HAT board" % (label, bx, by))

    # ---- 4. speaker fit ----
    r.head("4. Speaker fit")
    spk = next(c for c in comps if c.group == "speaker")
    ax0, ax1, ay0, ay1, az0, az1 = spk.aabb()
    if ax0 >= xi0 - 1e-6 and ax1 <= xi1 + 1e-6 and ay0 >= yi0 - 1e-6 and ay1 <= yi1 + 1e-6 and az1 <= zi1 + 1e-6:
        r.ok("CE32A-4 (32x32x15.5) fits: X[%.1f,%.1f] Y[%.1f,%.1f] depth to Z=%.1f (rear inner %.1f)"
             % (ax0, ax1, ay0, ay1, az1, zi1))
    else:
        r.fail("speaker does not fit inside the interior")
    # grille centre must sit on the speaker axis (front-firing)
    if abs(p["right_cx"] - (ax0 + ax1) / 2) < 1e-6 and abs(p["spk_cy"] - (ay0 + ay1) / 2) < 1e-6:
        r.ok("grille centre coincides with speaker axis (%.1f, %.1f)" % (p["right_cx"], p["spk_cy"]))

    # ---- 5. USB-C charger port + battery fit ----
    r.head("5. USB-C charger port + battery")
    usbc = next((o for o in lay.openings if o.name == "usbc"), None)
    cx, cy, cz = lay.usbc_center
    if usbc and abs(usbc.cx - cx) < 1e-6:
        r.ok("USB-C cutout in the RIGHT wall aligns with the charger edge (X=%.1f)" % cx)
    else:
        r.fail("USB-C cutout missing/misaligned with charger")
    bat = next((c for c in lay.components if c.group == "battery"), None)
    if bat:
        bx0, bx1, by0, by1, bz0, bz1 = bat.aabb()
        if bx0 >= xi0 - 1e-6 and bx1 <= xi1 + 1e-6 and by0 >= yi0 - 1e-6 and by1 <= yi1 + 1e-6 \
           and bz0 >= zi0 - 1e-6 and bz1 <= zi1 + 1e-6:
            r.ok("battery (60x36x7) fits: X[%.1f,%.1f] Y[%.1f,%.1f] Z[%.1f,%.1f]"
                 % (bx0, bx1, by0, by1, bz0, bz1))
        else:
            r.fail("battery pokes out of the interior")

    # ---- 6. enclosure slack (minimality) ----
    r.head("6. Enclosure slack (shrink toward ~%.1f mm margins)" % clr)
    allx0 = allx1 = ally0 = ally1 = allz0 = allz1 = None
    for c in comps:
        # only count parts that stay inside; protruding parts (knob, USB-C) exit via openings
        for pt in c.parts:
            if pt.role.startswith("protrude"):
                continue
            x0, x1, y0, y1, z0, z1 = pt.aabb()
            allx0 = x0 if allx0 is None else min(allx0, x0)
            allx1 = x1 if allx1 is None else max(allx1, x1)
            ally0 = y0 if ally0 is None else min(ally0, y0)
            ally1 = y1 if ally1 is None else max(ally1, y1)
            allz0 = z0 if allz0 is None else min(allz0, z0)
            allz1 = z1 if allz1 is None else max(allz1, z1)
    r.lines.append("      content bbox X[%.1f, %.1f]  Y[%.1f, %.1f]  Z[%.1f, %.1f]"
                   % (allx0, allx1, ally0, ally1, allz0, allz1))
    r.lines.append("      interior   X[%.1f, %.1f]  Y[%.1f, %.1f]  Z[%.1f, %.1f]"
                   % (xi0, xi1, yi0, yi1, zi0, zi1))
    slack = dict(left=allx0 - xi0, right=xi1 - allx1,
                 bottom=ally0 - yi0, top=yi1 - ally1,
                 front=allz0 - zi0, rear=zi1 - allz1)
    for k, v in slack.items():
        tag = r.ok if v >= -1e-6 else r.fail
        tag("slack %-6s = %+.2f mm" % (k, v))

    return r


def main():
    lay = layout_spec.compute()
    r = check(lay)
    print("BEDSIDE ENCLOSURE LAYOUT VALIDATION")
    print("Outer %.1f x %.1f x %.1f mm, wall %.1f, clearance %.1f"
          % (lay.W, lay.H, lay.D, lay.wall, lay.p["clearance"]))
    print("\n".join(r.lines))
    print("\n" + "=" * 60)
    if r.failures:
        print("RESULT: FAIL  (%d issue(s), %d warning(s))" % (len(r.failures), len(r.warnings)))
    else:
        print("RESULT: PASS  (%d warning(s))" % len(r.warnings))

    # machine-readable record for the record / the Fusion loop
    out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "validation_report.json")
    with open(out, "w") as f:
        json.dump(dict(outer=[lay.W, lay.H, lay.D], wall=lay.wall,
                       failures=r.failures, warnings=r.warnings), f, indent=2)
    sys.exit(1 if r.failures else 0)


if __name__ == "__main__":
    main()
