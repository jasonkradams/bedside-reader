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
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchLines.addCenterPointRectangle(
            adsk.core.Point3D.create(cx, cy, 0),
            adsk.core.Point3D.create(cx + w/2, cy + h/2, 0)
        )
        extInput = comp.features.extrudeFeatures.createInput(sk.profiles.item(0), adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        
        if z_offset == 0.0:
            extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(depth))
        else:
            # Create offset plane for starting the extrusion
            offInput = comp.constructionPlanes.createInput()
            offInput.setByOffset(plane, adsk.core.ValueInput.createByReal(z_offset))
            offPlane = comp.constructionPlanes.add(offInput)
            
            sk2 = comp.sketches.add(offPlane)
            sk2.sketchCurves.sketchLines.addCenterPointRectangle(
                adsk.core.Point3D.create(cx, cy, 0),
                adsk.core.Point3D.create(cx + w/2, cy + h/2, 0)
            )
            extInput2 = comp.features.extrudeFeatures.createInput(sk2.profiles.item(0), adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
            extInput2.setDistanceExtent(False, adsk.core.ValueInput.createByReal(depth))
            return comp.features.extrudeFeatures.add(extInput2)

        return comp.features.extrudeFeatures.add(extInput)

    def _extrude_cylinder(self, comp, plane, cx, cy, radius, depth, z_offset=0.0):
        if z_offset != 0.0:
            offInput = comp.constructionPlanes.createInput()
            offInput.setByOffset(plane, adsk.core.ValueInput.createByReal(z_offset))
            plane = comp.constructionPlanes.add(offInput)
            
        sk = comp.sketches.add(plane)
        sk.sketchCurves.sketchCircles.addByCenterRadius(
            adsk.core.Point3D.create(cx, cy, 0), radius
        )
        extInput = comp.features.extrudeFeatures.createInput(sk.profiles.item(0), adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        extInput.setDistanceExtent(False, adsk.core.ValueInput.createByReal(depth))
        return comp.features.extrudeFeatures.add(extInput)


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

    def build_speaker(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Speaker_CE32A-4", transform)
        xy = comp.xYConstructionPlane
        
        # Square Base Frame
        self._extrude_rect(comp, xy, 0, 0, 3.2, 3.2, 0.2, 0.0)
        
        # Cone (Cylinder pointing forward)
        self._extrude_cylinder(comp, xy, 0, 0, 1.5, 0.4, 0.2)
        
        # Magnet (Cylinder pointing backward)
        self._extrude_cylinder(comp, xy, 0, 0, 0.95, -0.95, 0.0)

    def build_audio_amp(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Audio_Amp_MAX98357A", transform)
        xy = comp.xYConstructionPlane
        
        # PCB
        self._extrude_rect(comp, xy, 0, 0, 1.94, 1.78, 0.2, 0.0)
        
        # Components bump
        self._extrude_rect(comp, xy, 0, 0, 1.5, 1.5, 0.1, 0.2)

    def build_encoder(self, transform: adsk.core.Matrix3D):
        comp = self.create_component("Rotary_Encoder_EC11", transform)
        xy = comp.xYConstructionPlane
        
        # Base body
        self._extrude_rect(comp, xy, 0, 0, 1.2, 1.2, -0.6, 0.0)
        
        # Threaded Neck
        self._extrude_cylinder(comp, xy, 0, 0, 0.35, 0.5, 0.0)
        
        # Shaft
        self._extrude_cylinder(comp, xy, 0, 0, 0.3, 1.5, 0.5)
        
        # Knob (Optional, visually represents the top)
        self._extrude_cylinder(comp, xy, 0, 0, 1.0, 1.5, 0.5)

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

        # 2. Speaker (Placed tightly to the right of the screen)
        # Main stack width is 6.55, so right edge is ~3.275
        # Speaker width is 3.2, so its left edge is ~1.6
        # Let's put speaker center at X = 3.275 + 1.6 + 0.2 (clearance) = 5.075
        mat_speaker = create_translation(5.1, 0, 0)
        builder.build_speaker(mat_speaker)

        # 3. Audio Amp (Placed behind the screen, slightly offset)
        # Pi Zero is at Z = -1.2 to -1.4. Let's put Amp at Z = -1.6
        mat_amp = create_translation(-1.5, 0, -1.6)
        builder.build_audio_amp(mat_amp)

        # 4. Rotary Encoder (Placed below the speaker, or to the right)
        # If speaker is at X=5.1, Y=0
        # Encoder can be at X=5.1, Y=-2.5
        mat_encoder = create_translation(5.1, -2.5, -0.6)
        builder.build_encoder(mat_encoder)

        # 5. Power Cable Keepout (Plugged into Pi Zero at bottom)
        # Pi Zero micro USB is roughly at X=1.0, Y=-1.5 (bottom edge)
        # We need to make sure the cable clears
        mat_cable = create_translation(1.0, -1.5, -1.3)
        builder.build_power_cable(mat_cable)

        app.activeViewport.fit()

    except Exception:
        if ui:
            ui.messageBox('Failed:\\n{}'.format(traceback.format_exc()))
