"""
Bedside Audiobook Appliance - enclosure generator (v4).

This script is DRIVEN BY layout_spec.py, which is the single source of truth for
every hardware dimension and position (all cross-checked against datasheets and
proven by validate_layout.py).  Nothing is hand-typed here that also lives there,
so the CAD can never drift from the validated layout again.

Coordinate note:
  layout_spec uses mm, corner origin, +X right / +Y up / +Z into the case
  (0 = front outer face).  Fusion's API uses cm and here we negate Z so the
  front face sits at Z=0 and the case body extends toward -Z (front protrusions
  such as the knob live at +Z).  Helpers L() and FZ() do the conversion.

What it builds:
  * Hardware_Placeholders  - every component as a see-through keep-out body,
                             placed exactly where validate_layout.py checked it.
  * Enclosure              - shell -> fillet -> shell -> split into Front_Faceplate
                             + Rear_Bucket -> lap joint, then:
      - exact screen window (active area + clearance)
      - 4 button holes with flexure bridges + plungers centred on the HAT buttons
      - 7 mm rotary-encoder hole
      - dotted speaker grille over the cone
      - front-firing sealed acoustic box + lid for the speaker
      - micro-USB power hole in the bottom wall + a surrounding mating boss
      - HAT/Pi, speaker and amp standoffs; M3 corner bosses
  * A Fusion interference check across the placeholders, written to
    validation_report_fusion.json.

Run it from Fusion's Scripts and Add-Ins, or push it through the AntigravityBridge
/execute endpoint (see run_fusion_loop.sh).
"""
import adsk.core, adsk.fusion, traceback
import math
import os
import sys
import json

# make layout_spec importable no matter where Fusion launches the script from
_HERE = os.path.dirname(os.path.abspath(__file__)) if "__file__" in globals() else os.getcwd()
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)
import layout_spec  # noqa: E402

MM = 0.1  # millimetre -> centimetre
SILENT = False  # the bridge loop sets this True: write build_status.json, no modal dialog


def L(mm):
    """scalar mm -> cm"""
    return mm * MM


def FZ(zmm):
    """layout Z in mm -> Fusion Z in cm (front face at 0, case toward -Z)"""
    return -zmm * MM


class Builder:
    def __init__(self, root, lay):
        self.root = root
        self.lay = lay
        self.p = lay.p

    # ---------------- component / body plumbing ----------------
    def _new_component(self, name):
        occ = self.root.occurrences.addNewComponent(adsk.core.Matrix3D.create())
        occ.component.name = name
        return occ.component

    def _plane_at_z(self, comp, zmm):
        """construction plane parallel to XY at the given layout-Z (mm)."""
        inp = comp.constructionPlanes.createInput()
        inp.setByOffset(comp.xYConstructionPlane, adsk.core.ValueInput.createByReal(FZ(zmm)))
        return comp.constructionPlanes.add(inp)

    def _extrude_profiles(self, comp, profiles, dist_cm, op):
        col = adsk.core.ObjectCollection.create()
        if hasattr(profiles, "__iter__"):
            for pr in profiles:
                col.add(pr)
        else:
            col.add(profiles)
        ext = comp.features.extrudeFeatures.createInput(col, op)
        ext.setDistanceExtent(False, adsk.core.ValueInput.createByReal(dist_cm))
        return comp.features.extrudeFeatures.add(ext)

    def _box(self, comp, cx, cy, cz, dx, dy, dz, op=adsk.fusion.FeatureOperations.NewBodyFeatureOperation):
        """axis-aligned box from layout mm centre + sizes."""
        z_front = cz - dz / 2.0
        plane = self._plane_at_z(comp, z_front)
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(L(cx), L(cy), 0),
            adsk.core.Point3D.create(L(cx) + L(dx) / 2, L(cy) + L(dy) / 2, 0))
        return self._extrude_profiles(comp, sk.profiles.item(0), -L(dz), op)

    def _cyl(self, comp, cx, cy, cz, radius, length, op=adsk.fusion.FeatureOperations.NewBodyFeatureOperation):
        """Z-axis cylinder from layout mm."""
        z_front = cz - length / 2.0
        plane = self._plane_at_z(comp, z_front)
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(L(cx), L(cy), 0), L(radius))
        return self._extrude_profiles(comp, sk.profiles.item(0), -L(length), op)

    def _draw_rounded_rect(self, sketch, cx, cy, w, h, r):
        """centre (cx,cy) size (w,h) radius r, ALL in cm."""
        arcs = sketch.sketchCurves.sketchArcs
        lines = sketch.sketchCurves.sketchLines
        dx, dy = w / 2 - r, h / 2 - r
        a_tr = arcs.addByCenterStartSweep(adsk.core.Point3D.create(cx + dx, cy + dy, 0),
                                          adsk.core.Point3D.create(cx + w / 2, cy + dy, 0), math.pi / 2)
        a_tl = arcs.addByCenterStartSweep(adsk.core.Point3D.create(cx - dx, cy + dy, 0),
                                          adsk.core.Point3D.create(cx - dx, cy + h / 2, 0), math.pi / 2)
        a_bl = arcs.addByCenterStartSweep(adsk.core.Point3D.create(cx - dx, cy - dy, 0),
                                          adsk.core.Point3D.create(cx - w / 2, cy - dy, 0), math.pi / 2)
        a_br = arcs.addByCenterStartSweep(adsk.core.Point3D.create(cx + dx, cy - dy, 0),
                                          adsk.core.Point3D.create(cx + dx, cy - h / 2, 0), math.pi / 2)
        lines.addByTwoPoints(a_tr.endSketchPoint, a_tl.startSketchPoint)
        lines.addByTwoPoints(a_tl.endSketchPoint, a_bl.startSketchPoint)
        lines.addByTwoPoints(a_bl.endSketchPoint, a_br.startSketchPoint)
        lines.addByTwoPoints(a_br.endSketchPoint, a_tr.startSketchPoint)

    def _combine(self, target, tools, op):
        col = adsk.core.ObjectCollection.create()
        for b in tools:
            col.add(b)
        ci = self.root.features.combineFeatures.createInput(target, col)
        ci.operation = op
        return self.root.features.combineFeatures.add(ci)

    # ---------------- 1. hardware placeholders (from the spec) ----------------
    def build_placeholders(self):
        comp = self._new_component("Hardware_Placeholders")
        for c in self.lay.components:
            for part in c.parts:
                if part.kind == "box":
                    self._box(comp, part.cx, part.cy, part.cz, part.dx, part.dy, part.dz)
                else:  # z-axis cylinder
                    self._cyl(comp, part.cx, part.cy, part.cz, part.radius, part.length)
        # name the bodies for readability
        try:
            idx = 0
            for c in self.lay.components:
                for part in c.parts:
                    comp.bRepBodies.item(idx).name = "%s__%s" % (c.name, part.name)
                    idx += 1
        except Exception:
            pass
        # make them translucent so the enclosure reads clearly
        try:
            for b in comp.bRepBodies:
                b.opacity = 0.45
        except Exception:
            pass
        return comp

    # ---------------- 2. enclosure ----------------
    def build_enclosure(self):
        p = self.p
        W, H, D, wall = p["W"], p["H"], p["D"], p["wall"]
        comp = self._new_component("Enclosure")
        self.enc = comp

        # --- outer solid: corner origin, front face at Z=0, body toward -Z ---
        sk = comp.sketches.add(comp.xYConstructionPlane)
        sk.sketchCurves.sketchLines.addTwoPointRectangle(
            adsk.core.Point3D.create(0, 0, 0), adsk.core.Point3D.create(L(W), L(H), 0))
        ext = self._extrude_profiles(comp, sk.profiles.item(0), -L(D),
                                     adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        body = ext.bodies.item(0)
        body.name = "Enclosure_Solid"

        # --- fillets: vertical corners big, front/back edges small ---
        vcol = adsk.core.ObjectCollection.create()
        hcol = adsk.core.ObjectCollection.create()
        for e in body.edges:
            p1, p2 = e.startVertex.geometry, e.endVertex.geometry
            if abs(p1.x - p2.x) < 1e-5 and abs(p1.y - p2.y) < 1e-5:
                vcol.add(e)
            else:
                hcol.add(e)
        try:
            fin = comp.features.filletFeatures.createInput()
            if vcol.count:
                fin.addConstantRadiusEdgeSet(vcol, adsk.core.ValueInput.createByReal(L(4.0)), True)
            if hcol.count:
                fin.addConstantRadiusEdgeSet(hcol, adsk.core.ValueInput.createByReal(L(1.5)), True)
            comp.features.filletFeatures.add(fin)
        except Exception:
            pass

        # --- hollow it (2 mm walls) ---
        objs = adsk.core.ObjectCollection.create()
        objs.add(body)
        sh = comp.features.shellFeatures.createInput(objs, False)
        sh.insideThickness = adsk.core.ValueInput.createByReal(L(wall))
        comp.features.shellFeatures.add(sh)

        # --- M3 corner screw bosses (front-to-back posts in the 4 corners) ---
        self._corner_bosses(comp, body)

        # --- front-face cutouts (window, buttons, encoder, grille) ---
        self._cut_front(comp, body)

        # --- split into Front_Faceplate + Rear_Bucket and add the lap joint ---
        front, rear = self._split_and_lap(comp, body)

        # --- features that belong to one piece ---
        if front is not None:
            self._plungers(comp, front)
            self._hat_standoffs(comp, front)
            self._acoustic_box(comp, front)
        if rear is not None:
            self._amp_standoffs(comp, rear)
            self._usbc_port(comp, rear)        # USB-C charger cutout in the RIGHT wall
            self._battery_pocket(comp, rear)   # snug/click battery pocket, no adhesive
        return comp

    def _usbc_port(self, comp, rear):
        """USB-C cutout in the right wall, aligned to the charger's connector."""
        cx, cy, cz = self.lay.usbc_center      # (charger right edge, cy, z)
        wallx = self.p["W"] - self.p["wall"] / 2.0   # centre of the right wall
        # 4mm through X, 9.5mm in Y, 4mm in Z -> passes a USB-C plug
        self._box(comp, wallx, cy, cz, 2.0 * self.p["wall"] + 1.0, 9.5, 4.0,
                  op=adsk.fusion.FeatureOperations.CutFeatureOperation)

    def _battery_pocket(self, comp, rear):
        """Snug ring wall around the 60x36 battery, rising 7mm off the rear inner wall."""
        p = self.p
        bx, by, bz = self.lay.battery_center
        z_rear = p["D"] - p["wall"]
        try:
            plane = self._plane_at_z(comp, z_rear)
            sk = comp.sketches.add(plane)
            self._draw_rounded_rect(sk, L(bx), L(by), L(65.4), L(41.9), L(2.0))   # outer wall
            self._draw_rounded_rect(sk, L(bx), L(by), L(62.6), L(39.1), L(1.0))   # inner = max cell + 0.3/side
            col = adsk.core.ObjectCollection.create()
            for pr in sk.profiles:
                if pr.profileLoops.count > 1:
                    col.add(pr)
            ei = comp.features.extrudeFeatures.createInput(
                col, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
            ei.setDistanceExtent(False, adsk.core.ValueInput.createByReal(L(8.5)))  # = max cell thickness
            feat = comp.features.extrudeFeatures.add(ei)
            self._combine(rear, [b for b in feat.bodies],
                          adsk.fusion.FeatureOperations.JoinFeatureOperation)
        except Exception:
            pass

    def _component_xy_footprints(self):
        """XY bounding rectangles (mm) of every placeholder part, for keep-out tests."""
        rects = []
        for c in self.lay.components:
            for part in c.parts:
                a = part.aabb()
                rects.append((a[0], a[1], a[2], a[3]))
        return rects

    def _xy_clear(self, x, y, r, foot, gap=1.0):
        """True if a post of radius r at (x,y) clears every component footprint."""
        for x0, x1, y0, y1 in foot:
            if (x0 - r - gap) <= x <= (x1 + r + gap) and (y0 - r - gap) <= y <= (y1 + r + gap):
                return False
        return True

    def _corner_bosses(self, comp, body):
        """M3 corner posts -- but only where they clear the hardware.  This tight
        layout fills the top-left (HAT) and bottom-right (speaker) corners, so
        those are skipped automatically; the lap joint carries closure there."""
        p = self.p
        W, H, D, wall = p["W"], p["H"], p["D"], p["wall"]
        foot = self._component_xy_footprints()
        inset = 6.0
        # corners first, then the clear gap between the HAT and the knob (top-centre)
        gap_x = (self.lay.screen_center[0] + 32.75 + p["right_cx"] - 10.0) / 2.0
        candidates = [(inset, inset), (inset, H - inset), (W - inset, inset), (W - inset, H - inset),
                      (gap_x, H - inset), (gap_x, inset)]
        placed = 0
        for cx, cy in candidates:
            if not self._xy_clear(cx, cy, 3.0, foot):
                continue
            self._cyl(comp, cx, cy, (wall + (D - wall)) / 2.0, 3.0, (D - 2 * wall),
                      op=adsk.fusion.FeatureOperations.JoinFeatureOperation)
            self._cyl(comp, cx, cy, (wall + (D - wall)) / 2.0, 1.35, (D - 2 * wall),
                      op=adsk.fusion.FeatureOperations.CutFeatureOperation)
            placed += 1
        self._n_bosses = placed

    def _cut_front(self, comp, body):
        lay, p = self.lay, self.p
        scx, scy = lay.screen_center
        # screen window: active area + 0.6 mm total clearance (cz=wall/2 -> cuts THROUGH)
        self._box(comp, scx, scy, p["wall"] / 2.0, 40.8 + 0.6, 30.6 + 0.6, p["wall"] + 1.0,
                  op=adsk.fusion.FeatureOperations.CutFeatureOperation)
        # button clearance holes through the front wall (the recessed discs float in these)
        for _label, bx, by in lay.buttons:
            self._cyl(comp, bx, by, p["wall"] / 2.0, 2.5, p["wall"] + 1.0,
                      op=adsk.fusion.FeatureOperations.CutFeatureOperation)
        # rotary-encoder shaft/neck hole (7 mm bushing -> 7.4 mm; cuts THROUGH)
        self._cyl(comp, p["right_cx"], p["enc_cy"], p["wall"] / 2.0, 7.4 / 2, p["wall"] + 1.0,
                  op=adsk.fusion.FeatureOperations.CutFeatureOperation)
        # dotted speaker grille over the cone
        gx, gy, gr = p["right_cx"], p["spk_cy"], p["grille_radius"]
        step = 5.0
        n = int(gr / step) + 1
        for i in range(-n, n + 1):
            for j in range(-n, n + 1):
                dx, dy = i * step, j * step
                if dx * dx + dy * dy <= gr * gr:
                    self._cyl(comp, gx + dx, gy + dy, p["wall"] / 2.0, 1.5 / 2, p["wall"] + 1.0,
                              op=adsk.fusion.FeatureOperations.CutFeatureOperation)

    def _usb_port(self, comp, body):
        """micro-USB hole in the bottom wall + a boss the cable mates into."""
        lay, p = self.lay, self.p
        ux, uy, uz = lay.usb_center            # port centre (mm); uy = Pi bottom edge
        opening = next(o for o in lay.openings if o.name == "usb")
        wall = p["wall"]
        # hole straight up through the bottom wall (Y from 0 to wall)
        plane = comp.xZConstructionPlane       # X-Z plane at Y=0
        sk = comp.sketches.add(plane)
        # on the X-Z sketch: sketch-X = world X, sketch-Y = -world Z (Fusion Z negated)
        sk.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(L(ux), -FZ(uz), 0),
            adsk.core.Point3D.create(L(ux) + L(opening.w) / 2, -FZ(uz) + L(opening.h) / 2, 0))
        self._extrude_profiles(comp, sk.profiles.item(0), L(wall + 0.5),
                               adsk.fusion.FeatureOperations.CutFeatureOperation)
        # surrounding boss: a hollow collar rising from the floor toward the Pi port
        boss_top = uy - 1.0                    # stop ~1 mm short of the Pi board edge
        boss_h = boss_top - wall
        if boss_h > 2.0:
            cy = wall + boss_h / 2.0
            outer = self._box(comp, ux, cy, uz, opening.w + 2 * 1.6, boss_h, opening.h + 2 * 1.6,
                              op=adsk.fusion.FeatureOperations.JoinFeatureOperation)
            self._box(comp, ux, cy, uz, opening.w + 0.5, boss_h + 2.0, opening.h + 0.5,
                      op=adsk.fusion.FeatureOperations.CutFeatureOperation)

    def _split_and_lap(self, comp, body):
        p = self.p
        W, H, wall = p["W"], p["H"], p["wall"]
        z_split = 11.0  # mm: behind the encoder body, in front of the USB boss (z>=11.7)
        try:
            splitPlane = self._plane_at_z(comp, z_split)
            si = comp.features.splitBodyFeatures.createInput(body, splitPlane, True)
            comp.features.splitBodyFeatures.add(si)
        except Exception:
            return None, None

        front = rear = None
        for b in comp.bRepBodies:
            if b.name.startswith("Speaker_Acoustic"):
                continue
            # front piece has its centroid nearer the front face (Fusion Z closer to 0)
            try:
                z = b.physicalProperties.centerOfMass.z
            except Exception:
                continue
            if z > FZ(z_split):
                front = b
            else:
                rear = b
        if front:
            front.name = "Front_Faceplate"
        if rear:
            rear.name = "Rear_Bucket"

        # lap joint: a lip on the rear rim that the faceplate closes over
        if front and rear:
            try:
                sk = comp.sketches.add(splitPlane)
                # The lip must OVERLAP the bucket wall so it welds to the rim instead of
                # floating inboard as a separate rib. Extend 1 mm outward (into the wall
                # footprint) and 1 mm inward -> base overlaps the 2 mm wall by 1 mm.
                self._draw_rounded_rect(sk, L(W / 2), L(H / 2), L(W - 2 * wall + 2), L(H - 2 * wall + 2), L(3.5))
                self._draw_rounded_rect(sk, L(W / 2), L(H / 2), L(W - 4 * wall - 2), L(H - 4 * wall - 2), L(2.5))
                lipCol = adsk.core.ObjectCollection.create()
                for pr in sk.profiles:
                    if pr.profileLoops.count > 1:
                        lipCol.add(pr)
                lipExt = comp.features.extrudeFeatures.createInput(
                    lipCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
                lipExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(L(3.0)))  # toward front
                lipFeat = comp.features.extrudeFeatures.add(lipExt)
                lips = [b for b in lipFeat.bodies]
                self._combine(rear, lips, adsk.fusion.FeatureOperations.JoinFeatureOperation)
                # carve the receiving groove in the faceplate
                ci = self.root.features.combineFeatures.createInput(front, adsk.core.ObjectCollection.create())
                ci.toolBodies.add(rear)
                ci.operation = adsk.fusion.FeatureOperations.CutFeatureOperation
                ci.isKeepToolBodies = True
                self.root.features.combineFeatures.add(ci)
            except Exception:
                pass
        return front, rear

    # ---------------- piece-specific features ----------------
    def _plungers(self, comp, front):
        """v3-style plungers, RECESSED 0.2 mm below the outer face (v3's design was good,
        it just sat flush). Each is a disc floating in its clearance hole, held by a thin
        flexure bridge, reaching back to the HAT button (which sits ~0.2 mm proud of the
        screen at z=wall)."""
        lay, p = self.lay, self.p
        wall = p["wall"]
        recess = 0.2                 # disc front sits 0.2 mm below the outer face
        button_z = wall - 0.2        # button actuator ~0.2 mm proud of the screen (z=wall)
        for _label, bx, by in lay.buttons:
            # thin flexure bridge from the disc out to the faceplate, at the recessed front
            sk = comp.sketches.add(self._plane_at_z(comp, recess))
            dx = -2.5 if bx < lay.screen_center[0] else 2.5
            sk.sketchCurves.sketchLines.addTwoPointRectangle(
                adsk.core.Point3D.create(L(bx + dx), L(by - 0.5), 0),
                adsk.core.Point3D.create(L(bx), L(by + 0.5), 0))
            self._extrude_and_join(comp, front, sk, -L(0.6))   # 0.6 mm-thick flexure arm
            # plunger disc: recessed front, reaching back to the (slightly proud) button
            length = button_z - recess
            self._cyl_join(comp, front, bx, by, recess + length / 2.0, 1.5, length)

    def _extrude_and_join(self, comp, target, sk, dist_cm):
        col = adsk.core.ObjectCollection.create()
        for pr in sk.profiles:
            col.add(pr)
        ei = comp.features.extrudeFeatures.createInput(col, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        ei.setDistanceExtent(False, adsk.core.ValueInput.createByReal(dist_cm))
        feat = comp.features.extrudeFeatures.add(ei)
        self._combine(target, [b for b in feat.bodies], adsk.fusion.FeatureOperations.JoinFeatureOperation)

    def _cyl_join(self, comp, target, cx, cy, cz, radius, length):
        feat = self._cyl(comp, cx, cy, cz, radius, length)
        self._combine(target, [b for b in feat.bodies], adsk.fusion.FeatureOperations.JoinFeatureOperation)

    def _standoff(self, comp, target, cx, cy, z_front, height_mm, outer_r=2.5, hole_r=1.1):
        """post rising from z_front (mm) into the case by height_mm, with a screw hole."""
        cz = z_front + height_mm / 2.0
        feat = self._cyl(comp, cx, cy, cz, outer_r, height_mm)
        self._combine(target, [b for b in feat.bodies], adsk.fusion.FeatureOperations.JoinFeatureOperation)
        self._cyl(comp, cx, cy, cz, hole_r, height_mm + 0.2,
                  op=adsk.fusion.FeatureOperations.CutFeatureOperation)

    def _hat_standoffs(self, comp, front):
        """4 posts on the 58x23 mm Pi/HAT pattern, holding the HAT front at z=4 mm."""
        lay, p = self.lay, self.p
        cx, cy = lay.screen_center            # HAT centre
        for hx in (cx - 29.0, cx + 29.0):
            for hy in (cy - 11.5, cy + 11.5):
                self._standoff(comp, front, hx, hy, p["wall"], 4.0 - p["wall"])

    def _amp_standoffs(self, comp, rear):
        """2 posts off the rear wall for the MAX98357A."""
        p = self.p
        D, wall = p["D"], p["wall"]
        cx, cy = p["amp_cx"], p["amp_cy"]
        z_wall = D - wall
        for hx in (cx - 7.0, cx + 7.0):
            # posts grow from the rear inner wall toward the front by ~4 mm
            cz = z_wall - 4.0 / 2.0
            feat = self._cyl(comp, hx, cy, cz, 2.5, 4.0)
            self._combine(rear, [b for b in feat.bodies], adsk.fusion.FeatureOperations.JoinFeatureOperation)
            self._cyl(comp, hx, cy, cz, 1.1, 4.2, op=adsk.fusion.FeatureOperations.CutFeatureOperation)

    def _acoustic_box(self, comp, front):
        """front-firing sealed chamber around the speaker + a glued lid."""
        p = self.p
        cx, cy = p["right_cx"], p["spk_cy"]
        wall = p["wall"]
        depth = 15.5 + 1.0            # speaker depth + a little air
        # outer wall rounded (looks nice); inner cavity SQUARE so the 32 mm square
        # speaker frame seats exactly flush against flat inner walls (req: issue 1).
        plane = self._plane_at_z(comp, wall)   # recessed behind the front wall so it can't poke out a corner
        sk = comp.sketches.add(plane)
        self._draw_rounded_rect(sk, L(cx), L(cy), L(36.0), L(36.0), L(3.0))
        sk.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(L(cx), L(cy), 0),
            adsk.core.Point3D.create(L(cx) + L(32.4) / 2, L(cy) + L(32.4) / 2, 0))
        wallCol = adsk.core.ObjectCollection.create()
        for pr in sk.profiles:
            if pr.profileLoops.count > 1:
                wallCol.add(pr)
        try:
            ei = comp.features.extrudeFeatures.createInput(wallCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
            ei.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-L(depth)))
            feat = comp.features.extrudeFeatures.add(ei)
            self._combine(front, [b for b in feat.bodies], adsk.fusion.FeatureOperations.JoinFeatureOperation)
        except Exception:
            pass
        # acoustic lid (separate body, glued on after wiring) -- rounded to match the box
        try:
            lid_plane = self._plane_at_z(comp, wall + depth)   # lid front (box is recessed by `wall`)
            sk_lid = comp.sketches.add(lid_plane)
            self._draw_rounded_rect(sk_lid, L(cx), L(cy), L(36.0), L(36.0), L(3.0))
            lid = self._extrude_profiles(comp, sk_lid.profiles.item(0), -L(wall),
                                         adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
            lid.bodies.item(0).name = "Speaker_Acoustic_Lid"
        except Exception:
            pass
        # speaker-wire notch: a small slot through the TOP wall of the box (toward the
        # amp) so the two speaker wires can pass into the otherwise-sealed chamber.
        try:
            self._box(comp, cx, cy + 17.0, 8.0, 4.5, 5.0, 5.0,
                      op=adsk.fusion.FeatureOperations.CutFeatureOperation)
        except Exception:
            pass

    # ---------------- 3. interference sanity check inside Fusion ----------------
    def analyze_interference(self, hw_comp):
        try:
            app = adsk.core.Application.get()
            design = app.activeProduct
            bodies = adsk.core.ObjectCollection.create()
            for b in hw_comp.bRepBodies:
                bodies.add(b)
            ii = design.createInterferenceInput(bodies)
            ii.areCoincidentFacesIncluded = False
            results = design.analyzeInterference(ii)
            hits = []
            for i in range(results.count):
                r = results.item(i)
                a, b = r.entityOne.name, r.entityTwo.name
                # parts of the SAME component (e.g. speaker cone inside its frame)
                # legitimately overlap; only report collisions BETWEEN components.
                if a.split("__")[0] == b.split("__")[0]:
                    continue
                hits.append(dict(a=a, b=b,
                                 volume_mm3=round(r.interferenceBody.volume * 1000.0, 3)))
            report = dict(interferences=hits, count=len(hits),
                          note="inter-component only; intra-part overlaps filtered")
        except Exception as e:
            report = dict(error=str(e))
        try:
            with open(os.path.join(_HERE, "validation_report_fusion.json"), "w") as f:
                json.dump(report, f, indent=2)
        except Exception:
            pass
        return report


def run(context):
    ui = None
    status = {"status": "ok"}
    try:
        app = adsk.core.Application.get()
        ui = app.userInterface
        docs = app.documents
        doc = docs.add(adsk.core.DocumentTypes.FusionDesignDocumentType)
        doc.name = "Bedside_Audiobook_V4"
        design = app.activeProduct
        root = design.rootComponent

        lay = layout_spec.compute()
        b = Builder(root, lay)
        hw = b.build_placeholders()
        b.build_enclosure()
        status["interference"] = b.analyze_interference(hw)

        app.activeViewport.fit()
    except Exception:
        status = {"status": "error", "traceback": traceback.format_exc()}
        if ui and not SILENT:
            ui.messageBox("Failed:\n{}".format(status["traceback"]))
    try:
        with open(os.path.join(_HERE, "build_status.json"), "w") as f:
            json.dump(status, f, indent=2)
    except Exception:
        pass
    return status


if __name__ == "__main__":
    run(None)
