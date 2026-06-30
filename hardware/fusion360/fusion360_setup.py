import adsk.core, adsk.fusion, traceback
import math
from typing import List, Tuple, Optional

class EnclosureBuilder:
    def __init__(self, root: adsk.fusion.Component):
        self.root: adsk.fusion.Component = root
        self.extrudes: adsk.fusion.ExtrudeFeatures = root.features.extrudeFeatures
        self.planes: adsk.fusion.ConstructionPlanes = root.constructionPlanes
        self.fillets: adsk.fusion.FilletFeatures = root.features.filletFeatures
        self.shells: adsk.fusion.ShellFeatures = root.features.shellFeatures
        self.splits: adsk.fusion.SplitBodyFeatures = root.features.splitBodyFeatures
        self.combines: adsk.fusion.CombineFeatures = root.features.combineFeatures

        self.front_body: Optional[adsk.fusion.BRepBody] = None
        self.rear_body: Optional[adsk.fusion.BRepBody] = None

        # Dimensions (cm)
        self.W: float = 14.0
        self.H: float = 7.0
        self.D: float = 6.0
        self.wall: float = 0.2

    def _safe_join(self, target_body: adsk.fusion.BRepBody, extrude_input: adsk.fusion.ExtrudeFeatureInput) -> adsk.fusion.ExtrudeFeature:
        extrude_input.operation = adsk.fusion.FeatureOperations.NewBodyFeatureOperation
        ext = self.extrudes.add(extrude_input)
        tools = adsk.core.ObjectCollection.create()
        for b in ext.bodies:
            tools.add(b)
        if tools.count > 0:
            comb = self.combines.createInput(target_body, tools)
            comb.operation = adsk.fusion.FeatureOperations.JoinFeatureOperation
            self.combines.add(comb)
        return ext

    def _draw_rounded_rect(self, sketch: adsk.fusion.Sketch, cx: float, cy: float, w: float, h: float, r: float) -> None:
        arcs = sketch.sketchCurves.sketchArcs
        lines = sketch.sketchCurves.sketchLines
        dx = w/2 - r
        dy = h/2 - r

        c_tr = adsk.core.Point3D.create(cx+dx, cy+dy, 0)
        a_tr = arcs.addByCenterStartSweep(c_tr, adsk.core.Point3D.create(cx+w/2, cy+dy, 0), math.pi/2)

        c_tl = adsk.core.Point3D.create(cx-dx, cy+dy, 0)
        a_tl = arcs.addByCenterStartSweep(c_tl, adsk.core.Point3D.create(cx-dx, cy+h/2, 0), math.pi/2)

        c_bl = adsk.core.Point3D.create(cx-dx, cy-dy, 0)
        a_bl = arcs.addByCenterStartSweep(c_bl, adsk.core.Point3D.create(cx-w/2, cy-dy, 0), math.pi/2)

        c_br = adsk.core.Point3D.create(cx+dx, cy-dy, 0)
        a_br = arcs.addByCenterStartSweep(c_br, adsk.core.Point3D.create(cx+dx, cy-h/2, 0), math.pi/2)

        lines.addByTwoPoints(a_tr.endSketchPoint, a_tl.startSketchPoint)
        lines.addByTwoPoints(a_tl.endSketchPoint, a_bl.startSketchPoint)
        lines.addByTwoPoints(a_bl.endSketchPoint, a_br.startSketchPoint)
        lines.addByTwoPoints(a_br.endSketchPoint, a_tr.startSketchPoint)

    def _create_standoffs(self, target_body: adsk.fusion.BRepBody, plane: adsk.fusion.ConstructionPlane, center_x: float, center_y: float, pitch_x: float, pitch_y: float, height_cm: float) -> None:
        sk_so = self.root.sketches.add(plane)
        dx_list = [-pitch_x/2, pitch_x/2] if pitch_x > 0 else [0.0]
        dy_list = [-pitch_y/2, pitch_y/2] if pitch_y > 0 else [0.0]

        for dx in dx_list:
            for dy in dy_list:
                sk_so.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(center_x+dx, center_y+dy, 0), 0.25)
                sk_so.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(center_x+dx, center_y+dy, 0), 0.12)

        pCol = adsk.core.ObjectCollection.create()
        for p in sk_so.profiles:
            if p.profileLoops.count > 1:
                pCol.add(p)

        soExtInput = self.extrudes.createInput(pCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        dist = adsk.core.ValueInput.createByReal(height_cm)
        soExtInput.setDistanceExtent(False, dist)
        self._safe_join(target_body, soExtInput)

    def build_outer_shell(self) -> adsk.fusion.BRepBody:
        sk_main = self.root.sketches.add(self.root.xYConstructionPlane)
        sk_main.sketchCurves.sketchLines.addTwoPointRectangle(
            adsk.core.Point3D.create(0, 0, 0),
            adsk.core.Point3D.create(self.W, self.H, 0)
        )

        extInput = self.extrudes.createInput(sk_main.profiles.item(0), adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-self.D))
        ext_solid = self.extrudes.add(extInput)
        main_body = ext_solid.bodies.item(0)
        main_body.name = "Enclosure_Solid"

        # Ergonomic Fillet
        edgeCol = adsk.core.ObjectCollection.create()
        for edge in main_body.edges:
            edgeCol.add(edge)

        filletInput = self.fillets.createInput()
        filletInput.addConstantRadiusEdgeSet(edgeCol, adsk.core.ValueInput.createByReal(1.0), True)
        filletInput.isG2 = False
        try:
            self.fillets.add(filletInput)
        except:
            pass

        # Shell
        objCol = adsk.core.ObjectCollection.create()
        objCol.add(main_body)
        shellInput = self.shells.createInput(objCol, False)
        shellInput.insideThickness = adsk.core.ValueInput.createByReal(self.wall)
        self.shells.add(shellInput)

        # Internal Screw Bosses
        boss_input = self.planes.createInput()
        boss_input.setByOffset(self.root.xYConstructionPlane, adsk.core.ValueInput.createByReal(-self.D + self.wall))
        boss_plane = self.planes.add(boss_input)
        sk_boss = self.root.sketches.add(boss_plane)

        for cx in [1.0, self.W - 1.0]:
            for cy in [1.0, self.H - 1.0]:
                sk_boss.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(cx, cy, 0), 0.4)

        bCol = adsk.core.ObjectCollection.create()
        for p in sk_boss.profiles:
            bCol.add(p)

        bossExt = self.extrudes.createInput(bCol, adsk.fusion.FeatureOperations.JoinFeatureOperation)
        bossExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(self.D - 2*self.wall))
        self.extrudes.add(bossExt)

        return main_body

    def split_enclosure(self, main_body: adsk.fusion.BRepBody) -> adsk.fusion.ConstructionPlane:
        planeInput = self.planes.createInput()
        planeInput.setByOffset(self.root.xYConstructionPlane, adsk.core.ValueInput.createByReal(-1.2))
        splitPlane = self.planes.add(planeInput)

        splitInput = self.splits.createInput(main_body, splitPlane, True)
        self.splits.add(splitInput)

        b1 = self.root.bRepBodies.item(0)
        b2 = self.root.bRepBodies.item(1)
        if b1.physicalProperties.centerOfMass.z > b2.physicalProperties.centerOfMass.z:
            self.front_body, self.rear_body = b1, b2
        else:
            self.front_body, self.rear_body = b2, b1

        self.front_body.name = "Front_Faceplate"
        self.rear_body.name = "Rear_Bucket"
        return splitPlane

    def build_lap_joint(self, splitPlane: adsk.fusion.ConstructionPlane) -> None:
        sk_lip = self.root.sketches.add(splitPlane)
        self._draw_rounded_rect(sk_lip, self.W/2, self.H/2, self.W - 2*self.wall, self.H - 2*self.wall, 1.0 - self.wall)
        self._draw_rounded_rect(sk_lip, self.W/2, self.H/2, self.W - 2*self.wall - 0.2, self.H - 2*self.wall - 0.2, 1.0 - self.wall - 0.1)

        lipCol = adsk.core.ObjectCollection.create()
        for p in sk_lip.profiles:
            if p.profileLoops.count > 1:
                lipCol.add(p)

        lipExt = self.extrudes.createInput(lipCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        lipExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.2))
        lipFeature = self.extrudes.add(lipExt)

        lip_bodies = adsk.core.ObjectCollection.create()
        for b in lipFeature.bodies:
            lip_bodies.add(b)

        combJoin = self.combines.createInput(self.rear_body, lip_bodies)
        combJoin.operation = adsk.fusion.FeatureOperations.JoinFeatureOperation
        self.combines.add(combJoin)

        combCut = self.combines.createInput(self.front_body, adsk.core.ObjectCollection.create())
        combCut.toolBodies.add(self.rear_body)
        combCut.operation = adsk.fusion.FeatureOperations.CutFeatureOperation
        combCut.isKeepToolBodies = True
        self.combines.add(combCut)

    def cut_front_faceplate(self) -> adsk.fusion.ConstructionPlane:
        sk_front = self.root.sketches.add(self.root.xYConstructionPlane)
        circles = sk_front.sketchCurves.sketchCircles
        lines = sk_front.sketchCurves.sketchLines

        # Screen Window
        sx, sy = 4.75, 3.5
        sw, sh = 4.1, 3.1
        lines.addCenterPointRectangle(
            adsk.core.Point3D.create(sx, sy, 0),
            adsk.core.Point3D.create(sx + sw/2, sy + sh/2, 0)
        )

        # Buttons
        button_coords = [
            (-2.5, 0.95),
            (-2.5, -0.95),
            (2.5, 0.95),
            (2.5, -0.95)
        ]
        for bx, by in button_coords:
            circles.addByCenterRadius(adsk.core.Point3D.create(sx+bx, sy+by, 0), 0.25)

        # Grille
        gx_center, gy_center = 10.5, 3.5
        for i in [-1.2, -0.6, 0, 0.6, 1.2]:
            for j in [-1.2, -0.6, 0, 0.6, 1.2]:
                if i**2 + j**2 <= 1.5:
                    circles.addByCenterRadius(adsk.core.Point3D.create(gx_center+i, gy_center+j, 0), 0.15)

        profCol = adsk.core.ObjectCollection.create()
        for p in sk_front.profiles:
            profCol.add(p)
        cutFrontInput = self.extrudes.createInput(profCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        cutFrontInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-0.5))
        self.extrudes.add(cutFrontInput)

        # Button Bridges
        sk_bridge = self.root.sketches.add(self.root.xYConstructionPlane)
        for bx, by in button_coords:
            cx, cy = sx+bx, sy+by

            # Draw the 3mm plunger
            sk_bridge.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(cx, cy, 0), 0.15)

            bridge_width = 0.1
            dx = -0.25 if bx < 0 else 0.25
            sk_bridge.sketchCurves.sketchLines.addTwoPointRectangle(
                adsk.core.Point3D.create(cx + dx, cy - bridge_width/2, 0),
                adsk.core.Point3D.create(cx, cy + bridge_width/2, 0)
            )
        brCol = adsk.core.ObjectCollection.create()
        for p in sk_bridge.profiles:
            brCol.add(p)
        brExt = self.extrudes.createInput(brCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        brExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-0.15))
        self._safe_join(self.front_body, brExt)

        sp_input = self.planes.createInput()
        sp_input.setByOffset(self.root.xYConstructionPlane, adsk.core.ValueInput.createByReal(-self.wall))
        screen_plane = self.planes.add(sp_input)

        # Screen Cradle
        sk_cradle = self.root.sketches.add(screen_plane)
        sk_cradle.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(sx, sy, 0), adsk.core.Point3D.create(sx + 3.3, sy + 1.75, 0)
        )
        sk_cradle.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(sx, sy, 0), adsk.core.Point3D.create(sx + 3.5, sy + 1.95, 0)
        )
        crCol = adsk.core.ObjectCollection.create()
        for p in sk_cradle.profiles:
            if p.profileLoops.count > 1:
                crCol.add(p)
        crExt = self.extrudes.createInput(crCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        crExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-0.5))
        self._safe_join(self.front_body, crExt)

        # Screen Standoffs (Pi mounting holes)
        self._create_standoffs(self.front_body, screen_plane, sx, sy, 5.8, 2.3, -0.4)

        return screen_plane

    def cut_rear_bucket(self) -> None:
        rp_input = self.planes.createInput()
        rp_input.setByOffset(self.root.xYConstructionPlane, adsk.core.ValueInput.createByReal(-self.D))
        rear_plane = self.planes.add(rp_input)
        sk_rear = self.root.sketches.add(rear_plane)

        ux, uy = 11.2, 1.0
        sk_rear.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(ux, uy, 0),
            adsk.core.Point3D.create(ux + 0.5, uy + 0.25, 0)
        )
        sk_rear.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(ux - 0.9, uy, 0), 0.15)
        sk_rear.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(ux + 0.9, uy, 0), 0.15)

        rpCol = adsk.core.ObjectCollection.create()
        for p in sk_rear.profiles:
            rpCol.add(p)
        cutRearInput = self.extrudes.createInput(rpCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        cutRearInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.5))
        self.extrudes.add(cutRearInput)

        sk_cb = self.root.sketches.add(rear_plane)
        for cx in [1.0, self.W - 1.0]:
            for cy in [1.0, self.H - 1.0]:
                sk_cb.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(cx, cy, 0), 0.3)
        cbCol = adsk.core.ObjectCollection.create()
        for p in sk_cb.profiles:
            cbCol.add(p)
        cbInput = self.extrudes.createInput(cbCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        cbInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.3))
        self.extrudes.add(cbInput)

        sk_clear = self.root.sketches.add(rear_plane)
        for cx in [1.0, self.W - 1.0]:
            for cy in [1.0, self.H - 1.0]:
                sk_clear.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(cx, cy, 0), 0.16)
        clCol = adsk.core.ObjectCollection.create()
        for p in sk_clear.profiles:
            clCol.add(p)
        clInput = self.extrudes.createInput(clCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        clInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(4.8))
        self.extrudes.add(clInput)

        sk_tap = self.root.sketches.add(rear_plane)
        for cx in [1.0, self.W - 1.0]:
            for cy in [1.0, self.H - 1.0]:
                sk_tap.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(cx, cy, 0), 0.14)
        tapCol = adsk.core.ObjectCollection.create()
        for p in sk_tap.profiles:
            tapCol.add(p)
        tapInput = self.extrudes.createInput(tapCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        tapInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(5.5))
        self.extrudes.add(tapInput)

        # Top Panel Rotary Hole
        top_input = self.planes.createInput()
        top_input.setByOffset(self.root.xZConstructionPlane, adsk.core.ValueInput.createByReal(self.H))
        top_plane = self.planes.add(top_input)
        sk_top = self.root.sketches.add(top_plane)
        sk_top.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(10.5, 3.0, 0), 0.35)
        topCol = adsk.core.ObjectCollection.create()
        for p in sk_top.profiles:
            topCol.add(p)
        topCut = self.extrudes.createInput(topCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        topCut.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-0.5))
        self.extrudes.add(topCut)

    def build_internal_acoustics(self, screen_plane: adsk.fusion.ConstructionPlane) -> None:
        gx_center, gy_center = 10.5, 3.5
        sk_speaker = self.root.sketches.add(screen_plane)
        sk_speaker.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(gx_center, gy_center, 0), adsk.core.Point3D.create(gx_center + 1.6, gy_center + 1.6, 0)
        )
        sk_speaker.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(gx_center, gy_center, 0), adsk.core.Point3D.create(gx_center + 1.75, gy_center + 1.75, 0)
        )
        spkCol = adsk.core.ObjectCollection.create()
        for p in sk_speaker.profiles:
            if p.profileLoops.count > 1:
                spkCol.add(p)
        spkExt = self.extrudes.createInput(spkCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        spkExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-1.5))
        self._safe_join(self.front_body, spkExt)

        self._create_standoffs(self.front_body, screen_plane, gx_center, gy_center, 2.6, 2.6, -0.2)

        notch_input = self.planes.createInput()
        notch_input.setByOffset(self.root.xYConstructionPlane, adsk.core.ValueInput.createByReal(-1.7))
        notch_plane = self.planes.add(notch_input)
        sk_notch = self.root.sketches.add(notch_plane)
        sk_notch.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(gx_center - 1.675, gy_center, 0),
            adsk.core.Point3D.create(gx_center - 1.675 + 0.1, gy_center + 0.15, 0)
        )
        notchCol = adsk.core.ObjectCollection.create()
        notchCol.add(sk_notch.profiles.item(0))
        notchExt = self.extrudes.createInput(notchCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
        notchExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.2))
        notchExt.participantBodies = [self.front_body]
        self.extrudes.add(notchExt)

        lid_input = self.planes.createInput()
        lid_input.setByOffset(self.root.xYConstructionPlane, adsk.core.ValueInput.createByReal(-1.7))
        lid_plane = self.planes.add(lid_input)
        sk_lid = self.root.sketches.add(lid_plane)
        sk_lid.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(gx_center, gy_center, 0), adsk.core.Point3D.create(gx_center + 1.75, gy_center + 1.75, 0)
        )
        lCol = adsk.core.ObjectCollection.create()
        lCol.add(sk_lid.profiles.item(0))
        lidExt = self.extrudes.createInput(lCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        lidExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-0.2))
        lid_feature = self.extrudes.add(lidExt)
        lid_body = lid_feature.bodies.item(0)
        lid_body.name = "Speaker_Acoustic_Lid"

    def build_floor_mounts(self) -> None:
        floor_input = self.planes.createInput()
        floor_input.setByOffset(self.root.xZConstructionPlane, adsk.core.ValueInput.createByReal(self.wall))
        floor_plane = self.planes.add(floor_input)

        # Audio Amp Standoffs
        self._create_standoffs(self.rear_body, floor_plane, 10.0, 3.0, 1.4, 0.0, 0.5)


def run(context):
    ui = None
    try:
        app = adsk.core.Application.get()
        ui = app.userInterface
        docs = app.documents

        new_doc = docs.add(adsk.core.DocumentTypes.FusionDesignDocumentType)
        new_doc.name = 'Bedside_Audiobook_Appliance_Auto'
        design = app.activeProduct
        root = design.rootComponent

        builder = EnclosureBuilder(root)

        main_body = builder.build_outer_shell()
        split_plane = builder.split_enclosure(main_body)
        screen_plane = builder.cut_front_faceplate()
        builder.cut_rear_bucket()
        builder.build_internal_acoustics(screen_plane)
        builder.build_floor_mounts()

        app.activeViewport.fit()
        ui.messageBox('Automated Enclosure Generation Complete!\nCheck the Timeline for the step-by-step features.')

    except Exception:
        if ui:
            ui.messageBox('Failed:\\n{}'.format(traceback.format_exc()))
