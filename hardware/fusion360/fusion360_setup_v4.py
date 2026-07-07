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

    def build_main_stack(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Main_Compute_Stack", transform)
        xy = comp.xYConstructionPlane
        
        # Display PCB (6.55 x 3.5 x 0.2)
        self._extrude_rect(comp, xy, 0, 0, 6.55, 3.5, 0.2, 0.0)
        
        # Screen Bump (4.2 x 3.2 x 0.1)
        self._extrude_rect(comp, xy, 0, 0, 4.2, 3.2, 0.1, 0.2)
        
        # Pi Zero PCB (6.5 x 3.0 x 0.2)
        self._extrude_rect(comp, xy, 0, 0, 6.5, 3.0, 0.2, -1.2)
        
        # GPIO Header block joining them
        self._extrude_rect(comp, xy, 0, 1.0, 5.0, 0.5, 1.2, -1.2)

        # Mounting Holes (4x M2.5, 58x23mm spacing -> 2.9cm and 1.15cm offsets)
        for hx in [-2.9, 2.9]:
            for hy in [-1.15, 1.15]:
                # Cut through everything (Z: 0.5 to -1.5)
                self._cut_cylinder(comp, xy, hx, hy, 0.125, -2.0, 0.5)

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
        for hx in [-1.3, 1.3]:
            for hy in [-1.3, 1.3]:
                # Cut through the base frame
                self._cut_cylinder(comp, xy, hx, hy, 0.1, -0.4, 0.2)

    def build_audio_amp(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Audio_Amp_MAX98357A", transform)
        xy = comp.xYConstructionPlane
        
        # PCB
        self._extrude_rect(comp, xy, 0, 0, 1.94, 1.78, 0.2, 0.0)
        
        # Components bump
        self._extrude_rect(comp, xy, 0, 0, 1.5, 1.5, 0.1, 0.2)

        # Mounting Holes (2x M2.5, 12mm spacing -> 0.6cm offsets)
        for hx in [-0.6, 0.6]:
            self._cut_cylinder(comp, xy, hx, 0, 0.125, -0.4, 0.2)

    def build_encoder(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Rotary_Encoder_EC11", transform)
        xy = comp.xYConstructionPlane
        
        # Base body (Sits BEHIND the panel: Z=0 to Z=-0.6)
        self._extrude_rect(comp, xy, 0, 0, 1.2, 1.2, -0.6, 0.0)
        
        # Threaded Neck (Mounts THROUGH the panel: Z=0 to Z=0.5)
        self._extrude_cylinder(comp, xy, 0, 0, 0.35, 0.5, 0.0)
        
        # Shaft (Protrudes further: Z=0.5 to Z=1.5)
        self._extrude_cylinder(comp, xy, 0, 0, 0.3, 1.0, 0.5)
        
        # Knob (Optional, visually represents the top: Z=1.0 to Z=1.5)
        self._extrude_cylinder(comp, xy, 0, 0, 1.0, 0.5, 1.0)

    def build_power_cable(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Power_Cable_Keepout", transform)
        xy = comp.xYConstructionPlane
        
        # Plastic Plug body
        self._extrude_rect(comp, xy, 0, 0, 1.15, 0.7, 2.0, 0.0)

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

        # 1. Main Stack (Origin)
        mat_stack = create_translation(0, 0, 0)
        builder.build_main_stack(mat_stack)

        # 2. Speaker (Placed horizontally next to the screen)
        # Main stack extends to X=3.275. Speaker width is 3.2 (extends to X=1.6).
        # We want a clean gap, say ~0.6cm (6mm).
        # cx = 3.275 + 1.6 + 0.6 = 5.475 -> ~5.5
        mat_speaker = create_translation(5.5, 0, 0)
        builder.build_speaker(mat_speaker)

        # 3. Rotary Encoder (Placed horizontally next to the speaker)
        # Speaker extends to X = 5.5 + 1.6 = 7.1.
        # Encoder knob is 2.0 diameter (extends to X=1.0).
        # Gap ~0.4cm.
        # cx = 7.1 + 1.0 + 0.4 = 8.5
        mat_encoder = create_translation(8.5, 0, 0)
        builder.build_encoder(mat_encoder)

        # 4. Audio Amp (Placed behind the empty space between Screen and Speaker)
        # Pi Zero is at Z = -1.2 to -1.4. Let's put Amp at Z = -1.5
        mat_amp = create_translation(3.5, 0, -1.5)
        builder.build_audio_amp(mat_amp)

        # 5. Power Cable Keepout (Plugged into Pi Zero at bottom)
        mat_cable = create_translation(1.0, -1.5, -1.3)
        builder.build_power_cable(mat_cable)

        app.activeViewport.fit()

    except Exception:
        if ui:
            ui.messageBox('Failed:\n{}'.format(traceback.format_exc()))
