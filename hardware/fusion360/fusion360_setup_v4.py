import adsk.core, adsk.fusion, traceback
import math

class AssemblyBuilder:
    def __init__(self, root: adsk.fusion.Component):
        self.root = root
        self.app = adsk.core.Application.get()
        self.design = self.app.activeProduct

    def create_component(self, name: str, transform: adsk.core.Matrix3D) -> adsk.fusion.Component:
        occ = self.root.occurrences.addNewComponent(transform)
        comp = occ.component
        comp.name = name
        return comp

    def _extrude_rect(self, comp, plane, cx, cy, w, h, depth, z_offset=0.0):
        if z_offset != 0.0:
            offInput = comp.constructionPlanes.createInput()
            offInput.setByOffset(plane, adsk.core.ValueInput.createByReal(z_offset))
            plane = comp.constructionPlanes.add(offInput)
            
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(cx, cy, 0),
            adsk.core.Point3D.create(cx + w/2, cy + h/2, 0)
        )
        extInput = comp.features.extrudeFeatures.createInput(sk.profiles.item(0), adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(depth))
        return comp.features.extrudeFeatures.add(extInput)

    def _extrude_cylinder(self, comp, plane, cx, cy, radius, depth, z_offset=0.0, operation=adsk.fusion.FeatureOperations.NewBodyFeatureOperation):
        if z_offset != 0.0:
            offInput = comp.constructionPlanes.createInput()
            offInput.setByOffset(plane, adsk.core.ValueInput.createByReal(z_offset))
            plane = comp.constructionPlanes.add(offInput)
            
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchCircles.addByCenterRadius(
            adsk.core.Point3D.create(cx, cy, 0), radius
        )
        extInput = comp.features.extrudeFeatures.createInput(sk.profiles.item(0), operation)
        extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(depth))
        return comp.features.extrudeFeatures.add(extInput)

    def _cut_cylinder(self, comp, plane, cx, cy, radius, depth, z_offset=0.0):
        return self._extrude_cylinder(comp, plane, cx, cy, radius, depth, z_offset, adsk.fusion.FeatureOperations.CutFeatureOperation)

    def _cut_rect(self, comp, plane, cx, cy, w, h, depth, z_offset=0.0):
        if z_offset != 0.0:
            offInput = comp.constructionPlanes.createInput()
            offInput.setByOffset(plane, adsk.core.ValueInput.createByReal(z_offset))
            plane = comp.constructionPlanes.add(offInput)
            
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(cx, cy, 0),
            adsk.core.Point3D.create(cx + w/2, cy + h/2, 0)
        )
        extInput = comp.features.extrudeFeatures.createInput(sk.profiles.item(0), adsk.fusion.FeatureOperations.CutFeatureOperation)
        extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(depth))
        return comp.features.extrudeFeatures.add(extInput)

    def build_main_stack(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Main_Compute_Stack", transform)
        xy = comp.xYConstructionPlane
        
        # Display HAT PCB
        self._extrude_rect(comp, xy, 0, 0, 6.5, 3.0, 0.16, -0.36)
        
        # Screen Bump
        self._extrude_rect(comp, xy, 0.3, 0.0, 4.3, 3.3, 0.2, -0.2)
        
        # Pi Zero PCB
        self._extrude_rect(comp, xy, 0, 0, 6.5, 3.0, 0.16, -1.46)
        
        # GPIO Header block joining them
        self._extrude_rect(comp, xy, 0, 1.0, 5.0, 0.5, 1.1, -1.46)

        # Mounting Holes (4x M2.5, typical Pi Zero spacing is 58x23mm)
        # Cut from Z = -1.5 to Z = 0.5 (depth = 2.0, offset = -1.5)
        for hx in [-2.9, 2.9]:
            for hy in [-1.15, 1.15]:
                self._cut_cylinder(comp, xy, hx, hy, 0.125, 2.0, -1.5)

    def build_speaker(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Speaker_CE32A_4", transform)
        xy = comp.xYConstructionPlane
        
        # Square Base Frame
        self._extrude_rect(comp, xy, 0, 0, 3.2, 3.2, 0.2, 0.0)
        
        # Cone (Cylinder pointing forward)
        self._extrude_cylinder(comp, xy, 0, 0, 1.5, 0.4, 0.2)
        
        # Magnet (Cylinder pointing backward)
        self._extrude_cylinder(comp, xy, 0, 0, 0.95, -0.95, 0.0)

        # Mounting Holes (4x 2.0mm, ~26x26mm spacing -> 1.3cm offsets)
        # Mounting Holes (4x M3)
        # Cut from Z = -0.1 to Z = 0.3 (depth = 0.4, offset = -0.1)
        for hx in [-1.35, 1.35]:
            for hy in [-1.35, 1.35]:
                self._cut_cylinder(comp, xy, hx, hy, 0.15, 0.4, -0.1)

    def build_audio_amp(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Audio_Amp_MAX98357A", transform)
        xy = comp.xYConstructionPlane
        
        # 1. Main PCB (19.4mm x 17.8mm x 1.6mm)
        # We'll center it at 0,0
        self._extrude_rect(comp, xy, 0, 0, 1.94, 1.78, 0.16, 0.0)
        
        # 2. Black IC Chip (approx 3x3mm)
        self._extrude_rect(comp, xy, 0, -0.2, 0.3, 0.3, 0.1, 0.16)
        
        # 3. Terminal Block bump (top edge)
        self._extrude_rect(comp, xy, 0, 0.5, 0.8, 0.6, 0.6, 0.16)

        # 4. Mounting Holes (two M2.5 holes)
        # Bottom left and bottom right
        self._cut_cylinder(comp, xy, -0.7, -0.6, 0.125, 0.6, -0.1)
        self._cut_cylinder(comp, xy, 0.7, -0.6, 0.125, 0.6, -0.1)

    def build_encoder(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Rotary_Encoder_EC11", transform)
        xy = comp.xYConstructionPlane
        
        # Base body (Sits BEHIND the panel: Z=0 to Z=-0.6)
        self._extrude_rect(comp, xy, 0, 0, 1.2, 1.2, -0.6, 0.0)
        
        # Threaded Neck (Mounts THROUGH the panel: Z=0 to Z=0.5)
        self._extrude_cylinder(comp, xy, 0, 0, 0.35, 0.5, 0.0)
        
        # Shaft (Protrudes further: Z=0.5 to Z=1.5)
        self._extrude_cylinder(comp, xy, 0, 0, 0.3, 1.0, 0.5)

    def build_power_cable(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Power_Cable_Keepout", transform)
        xy = comp.xYConstructionPlane
        
        # Plastic Plug body
        self._extrude_rect(comp, xy, 0, 0, 1.15, 0.7, 2.0, 0.0)

class EnclosureBuilder(AssemblyBuilder):
    def _draw_rounded_rect(self, sketch, cx, cy, w, h, r):
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

    def build_enclosure(self):
        comp = self.create_component("Outer_Enclosure", adsk.core.Matrix3D.create())
        xy = comp.xYConstructionPlane
        
        # 1. Solid Bounding Box (cx=0.55, cy=-0.3, W=11.3, H=4.6)
        box_feature = self._extrude_rect(comp, xy, 0.55, -0.3, 11.3, 4.6, 3.2, -3.0)
        box_body = box_feature.bodies.item(0)
        
        # 1.5. Ergonomic Fillet
        vertCol = adsk.core.ObjectCollection.create()
        for edge in box_body.edges:
            p1 = edge.startVertex.geometry
            p2 = edge.endVertex.geometry
            if abs(p1.x - p2.x) < 1e-5 and abs(p1.y - p2.y) < 1e-5:
                vertCol.add(edge)
                
        if vertCol.count > 0:
            filletInput1 = comp.features.filletFeatures.createInput()
            filletInput1.addConstantRadiusEdgeSet(vertCol, adsk.core.ValueInput.createByReal(0.8), True)
            comp.features.filletFeatures.add(filletInput1)
            
        horizCol = adsk.core.ObjectCollection.create()
        for edge in box_body.edges:
            p1 = edge.startVertex.geometry
            p2 = edge.endVertex.geometry
            if not (abs(p1.x - p2.x) < 1e-5 and abs(p1.y - p2.y) < 1e-5):
                horizCol.add(edge)
                
        if horizCol.count > 0:
            filletInput2 = comp.features.filletFeatures.createInput()
            filletInput2.addConstantRadiusEdgeSet(horizCol, adsk.core.ValueInput.createByReal(0.2), True)
            comp.features.filletFeatures.add(filletInput2)
        
        # 2. Shell it (2mm walls)
        objs = adsk.core.ObjectCollection.create()
        objs.add(box_body)
        shellInput = comp.features.shellFeatures.createInput(objs, False)
        shellInput.insideThickness = adsk.core.ValueInput.createByReal(0.2)
        comp.features.shellFeatures.add(shellInput)
        
        # 3. Faceplate Cutouts (Screen and Grill)
        # Screen (4.3 x 3.3 to give 1mm clearance)
        self._cut_rect(comp, xy, 2.5, -0.5, 4.3, 3.3, 0.4, 0.0)
        
        # Speaker Grill (Diamond grid of holes)
        gx, gy = -3.0, -0.5
        for i in [-1.2, -0.6, 0, 0.6, 1.2]:
            for j in [-1.2, -0.6, 0, 0.6, 1.2]:
                if i**2 + j**2 <= 1.5:
                    self._cut_cylinder(comp, xy, gx + i, gy + j, 0.15, 0.4, 0.0)
                    
        # Screen Buttons & Plungers
        sx, sy = 2.5, -0.5
        button_coords = [(-2.6, 0.6), (-2.6, -0.5), (2.6, 0.6), (2.6, -0.5)]
        
        for bx, by in button_coords:
            self._cut_cylinder(comp, xy, sx+bx, sy+by, 0.25, 0.4, 0.0)
            
        sk_bridge = comp.sketches.add(xy)
        for bx, by in button_coords:
            cx, cy = sx+bx, sy+by
            dx = -0.25 if bx < 0 else 0.25
            sk_bridge.sketchCurves.sketchLines.addTwoPointRectangle(
                adsk.core.Point3D.create(cx + dx, cy - 0.05, 0),
                adsk.core.Point3D.create(cx, cy + 0.05, 0)
            )
        brCol = adsk.core.ObjectCollection.create()
        for p in sk_bridge.profiles:
            brCol.add(p)
        brExt = comp.features.extrudeFeatures.createInput(brCol, adsk.fusion.FeatureOperations.JoinFeatureOperation)
        brExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.06)) # 0.6mm thin flexure
        comp.features.extrudeFeatures.add(brExt)
        
        sk_plunger = comp.sketches.add(xy)
        for bx, by in button_coords:
            sk_plunger.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(sx+bx, sy+by, 0), 0.15)
        plCol = adsk.core.ObjectCollection.create()
        for p in sk_plunger.profiles:
            plCol.add(p)
        plExt = comp.features.extrudeFeatures.createInput(plCol, adsk.fusion.FeatureOperations.JoinFeatureOperation)
        plExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.17)) # 1.7mm thick plunger
        comp.features.extrudeFeatures.add(plExt)
                
        # 4. Top/Rear Cutouts
        xz = comp.xZConstructionPlane
        
        # Bottom Face Cutout for Power (X=4.65, Z=-1.38)
        offInputBot = comp.constructionPlanes.createInput()
        offInputBot.setByOffset(xz, adsk.core.ValueInput.createByReal(-2.6))
        xz_bottom = comp.constructionPlanes.add(offInputBot)
        
        sk_bottom = comp.sketches.add(xz_bottom)
        sk_bottom.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(4.65, -1.38, 0),
            adsk.core.Point3D.create(4.65 + 0.6, -1.38 + 0.3, 0)
        )
        objs_bot = adsk.core.ObjectCollection.create()
        for i in range(sk_bottom.profiles.count):
            objs_bot.add(sk_bottom.profiles.item(i))
            
        extInputBot = comp.features.extrudeFeatures.createInput(objs_bot, adsk.fusion.FeatureOperations.CutFeatureOperation)
        extInputBot.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.5)) # Cut 5mm inwards (+Y)
        # Assumes bodies named later
        comp.features.extrudeFeatures.add(extInputBot)

        offInput = comp.constructionPlanes.createInput()
        offInput.setByOffset(xz, adsk.core.ValueInput.createByReal(2.0))
        xz_top = comp.constructionPlanes.add(offInput)
        
        sk_top = comp.sketches.add(xz_top)
        # Encoder Hole (Global Z = -1.5 -> Sketch Y = 1.5)
        sk_top.sketchCurves.sketchCircles.addByCenterRadius(adsk.core.Point3D.create(-3.0, 1.5, 0), 0.4)
        
        objs_top = adsk.core.ObjectCollection.create()
        for i in range(sk_top.profiles.count):
            objs_top.add(sk_top.profiles.item(i))
            
        extInput = comp.features.extrudeFeatures.createInput(objs_top, adsk.fusion.FeatureOperations.CutFeatureOperation)
        extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-1.0))
        comp.features.extrudeFeatures.add(extInput)
        
        
        # 5. Component Standoffs
        # Pi Standoffs
        for hx in [2.5 - 2.9, 2.5 + 2.9]:
            for hy in [-0.5 - 1.15, -0.5 + 1.15]:
                self._extrude_cylinder(comp, xy, hx, hy, 0.25, -0.2, 0.0, adsk.fusion.FeatureOperations.JoinFeatureOperation)
                self._cut_cylinder(comp, xy, hx, hy, 0.1, -0.2, 0.0)
                
        # Speaker Standoffs
        for hx in [-3.0 - 1.35, -3.0 + 1.35]:
            for hy in [-0.5 - 1.35, -0.5 + 1.35]:
                self._extrude_cylinder(comp, xy, hx, hy, 0.25, -1.0, 0.0, adsk.fusion.FeatureOperations.JoinFeatureOperation)
                self._cut_cylinder(comp, xy, hx, hy, 0.1, -1.0, 0.0)
                
        # Audio Amp Standoffs
        pts = [ (2.5 - 0.7, -0.5 - 0.6), (2.5 + 0.7, -0.5 - 0.6) ]
        for px, py in pts:
            self._extrude_cylinder(comp, xy, px, py, 0.25, 0.3, -2.8, adsk.fusion.FeatureOperations.JoinFeatureOperation)
            self._cut_cylinder(comp, xy, px, py, 0.1, 0.3, -2.8)
        
        # 6. Split Case and Create Lap Joint
        main_body = None
        for b in comp.bRepBodies:
            if b.name != "Speaker_Acoustic_Lid":
                main_body = b
                break
                
        if main_body:
            planeInput = comp.constructionPlanes.createInput()
            planeInput.setByOffset(xy, adsk.core.ValueInput.createByReal(-1.2))
            splitPlane = comp.constructionPlanes.add(planeInput)

            splitInput = comp.features.splitBodyFeatures.createInput(main_body, splitPlane, True)
            comp.features.splitBodyFeatures.add(splitInput)
            
            front_body = None
            rear_body = None
            for b in comp.bRepBodies:
                if b.name != "Speaker_Acoustic_Lid":
                    if b.physicalProperties.centerOfMass.z > -1.2:
                        front_body = b
                    else:
                        rear_body = b
                        
            if front_body and rear_body:
                front_body.name = "Front_Faceplate"
                rear_body.name = "Rear_Bucket"
                
                # Build lap joint
                sk_lip = comp.sketches.add(splitPlane)
                self._draw_rounded_rect(sk_lip, 0.55, -0.3, 10.9, 4.2, 0.8)
                self._draw_rounded_rect(sk_lip, 0.55, -0.3, 10.5, 3.8, 0.6)
                
                lipCol = adsk.core.ObjectCollection.create()
                for p in sk_lip.profiles:
                    if p.profileLoops.count > 1:
                        lipCol.add(p)
                        
                lipExt = comp.features.extrudeFeatures.createInput(lipCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
                lipExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.2)) # extrude towards +Z
                lipFeature = comp.features.extrudeFeatures.add(lipExt)
                
                lip_bodies = adsk.core.ObjectCollection.create()
                for b in lipFeature.bodies:
                    lip_bodies.add(b)
                    
                combJoin = comp.features.combineFeatures.createInput(rear_body, lip_bodies)
                combJoin.operation = adsk.fusion.FeatureOperations.JoinFeatureOperation
                comp.features.combineFeatures.add(combJoin)
                
                combCut = comp.features.combineFeatures.createInput(front_body, adsk.core.ObjectCollection.create())
                combCut.toolBodies.add(rear_body)
                combCut.operation = adsk.fusion.FeatureOperations.CutFeatureOperation
                combCut.isKeepToolBodies = True
                comp.features.combineFeatures.add(combCut)
                
                # 7. Speaker Acoustic Box (Joined to Front Faceplate)
                gx, gy = -3.0, -0.5
                sk_walls = comp.sketches.add(xy)
                sk_walls.sketchCurves.sketchLines.addCenterPointRectangle(
                    adsk.core.Point3D.create(gx, gy, 0), adsk.core.Point3D.create(gx + 2.0, gy + 2.0, 0)
                )
                sk_walls.sketchCurves.sketchLines.addCenterPointRectangle(
                    adsk.core.Point3D.create(gx, gy, 0), adsk.core.Point3D.create(gx + 1.8, gy + 1.8, 0)
                )
                wallCol = adsk.core.ObjectCollection.create()
                for p in sk_walls.profiles:
                    if p.profileLoops.count > 1:
                        wallCol.add(p)
                wallExt = comp.features.extrudeFeatures.createInput(wallCol, adsk.fusion.FeatureOperations.JoinFeatureOperation)
                wallExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-1.8))
                wallExt.participantBodies = [front_body]
                comp.features.extrudeFeatures.add(wallExt)

                # Acoustic Lid
                lid_input = comp.constructionPlanes.createInput()
                lid_input.setByOffset(xy, adsk.core.ValueInput.createByReal(-1.8))
                lid_plane = comp.constructionPlanes.add(lid_input)

                sk_lid = comp.sketches.add(lid_plane)
                sk_lid.sketchCurves.sketchLines.addCenterPointRectangle(
                    adsk.core.Point3D.create(gx, gy, 0), adsk.core.Point3D.create(gx + 2.0, gy + 2.0, 0)
                )
                lCol = adsk.core.ObjectCollection.create()
                lCol.add(sk_lid.profiles.item(0))
                lidExt = comp.features.extrudeFeatures.createInput(lCol, adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
                lidExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(-0.2)) # Towards -Z
                lid_feature = comp.features.extrudeFeatures.add(lidExt)
                lid_body = lid_feature.bodies.item(0)
                lid_body.name = "Speaker_Acoustic_Lid"

                # Acoustic Lip
                sk_lip_spk = comp.sketches.add(lid_plane)
                sk_lip_spk.sketchCurves.sketchLines.addCenterPointRectangle(
                    adsk.core.Point3D.create(gx, gy, 0), adsk.core.Point3D.create(gx + 1.8, gy + 1.8, 0)
                )
                sk_lip_spk.sketchCurves.sketchLines.addCenterPointRectangle(
                    adsk.core.Point3D.create(gx, gy, 0), adsk.core.Point3D.create(gx + 1.6, gy + 1.6, 0)
                )
                lipColSpk = adsk.core.ObjectCollection.create()
                for p in sk_lip_spk.profiles:
                    if p.profileLoops.count > 1:
                        lipColSpk.add(p)
                lipExtSpk = comp.features.extrudeFeatures.createInput(lipColSpk, adsk.fusion.FeatureOperations.JoinFeatureOperation)
                lipExtSpk.setDistanceExtent(False, adsk.core.ValueInput.createByReal(0.4)) # Extrude into walls by 4mm (+Z)
                lipExtSpk.participantBodies = [lid_body]
                comp.features.extrudeFeatures.add(lipExtSpk)

                # Wire Notch (Right side, facing Pi)
                sk_notch = comp.sketches.add(lid_plane)
                sk_notch.sketchCurves.sketchLines.addCenterPointRectangle(
                    adsk.core.Point3D.create(gx + 1.8, gy, 0),
                    adsk.core.Point3D.create(gx + 1.8 - 0.3, gy + 0.3, 0)
                )
                notchCol = adsk.core.ObjectCollection.create()
                notchCol.add(sk_notch.profiles.item(0))
                notchExt = comp.features.extrudeFeatures.createInput(notchCol, adsk.fusion.FeatureOperations.CutFeatureOperation)
                notchExt.setDistanceExtent(False, adsk.core.ValueInput.createByReal(1.0))
                notchExt.participantBodies = [lid_body, front_body]
                comp.features.extrudeFeatures.add(notchExt)

def create_translation(x, y, z):
    mat = adsk.core.Matrix3D.create()
    mat.translation = adsk.core.Vector3D.create(x, y, z)
    return mat

def run(context):
    ui = None
    try:
        app = adsk.core.Application.get()
        ui = app.userInterface
        docs = app.documents

        new_doc = docs.add(adsk.core.DocumentTypes.FusionDesignDocumentType)
        new_doc.name = 'Bedside_Audiobook_V4_Components'
        design = app.activeProduct
        root = design.rootComponent

        builder = AssemblyBuilder(root)

        # 1. Main Stack (Screen on the right)
        # We will place its center at X = +2.5, Y = -0.5
        mat_stack = create_translation(2.5, -0.5, 0)
        builder.build_main_stack(mat_stack)

        # 2. Speaker (Left face)
        # Flip 180 degrees around Y so the magnet faces the front, and recess it into the case to Z = -1.0
        mat_speaker = adsk.core.Matrix3D.create()
        mat_speaker.setToRotation(math.pi, adsk.core.Vector3D.create(0, 1, 0), adsk.core.Point3D.create(0,0,0))
        mat_speaker.translation = adsk.core.Vector3D.create(-3.0, -0.5, -1.0)
        builder.build_speaker(mat_speaker)

        # 3. Rotary Encoder (Top left, pointing UP out the top of the box)
        # Rotate -90 degrees around X-axis so its local +Z (shaft) points to global +Y (up).
        mat_encoder = adsk.core.Matrix3D.create()
        mat_encoder.setToRotation(-math.pi / 2, adsk.core.Vector3D.create(1, 0, 0), adsk.core.Point3D.create(0,0,0))
        # Place it above the speaker (X=-3.0), at the top edge of the box (Y=2.0), and centered in depth (Z=-1.5)
        mat_encoder.translation = adsk.core.Vector3D.create(-3.0, 2.0, -1.5)
        builder.build_encoder(mat_encoder)

        # 4. Audio Amp
        # Place it flat against the back wall (Z = -2.5), directly behind the Pi Stack (X = 2.5, Y = -0.5).
        mat_amp = adsk.core.Matrix3D.create()
        mat_amp.translation = adsk.core.Vector3D.create(2.5, -0.5, -2.5)
        builder.build_audio_amp(mat_amp)

        # 5. Generate the Outer Enclosure Shell
        enclosure_builder = EnclosureBuilder(root)
        enclosure_builder.build_enclosure()

        app.activeViewport.fit()

    except Exception:
        if ui:
            ui.messageBox('Failed:\n{}'.format(traceback.format_exc()))
