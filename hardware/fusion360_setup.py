import adsk.core, adsk.fusion

def run(_context: str):
    app  = adsk.core.Application.get()
    docs = app.documents

    # ── 1. New Assembly document ─────────────────────────────────────────
    new_doc = docs.add(adsk.core.DocumentTypes.FusionDesignDocumentType)
    new_doc.name = 'Bedside_Audiobook_Appliance'

    design = adsk.fusion.Design.cast(
        new_doc.products.itemByProductType('DesignProductType'))
    root   = design.rootComponent
    params = design.userParameters

    # ── 2. User Parameters ───────────────────────────────────────────────
    param_defs = [
        # name                    mm value  comment
        ('Pi_Length',             65.0,  'Raspberry Pi Zero 2 W board length (mm)'),
        ('Pi_Width',              30.0,  'Raspberry Pi Zero 2 W board width (mm)'),
        ('Pi_Hole_Spacing_X',     58.0,  'Pi mounting hole X-axis centre-to-centre (mm)'),
        ('Pi_Hole_Spacing_Y',     23.0,  'Pi mounting hole Y-axis centre-to-centre (mm)'),
        ('Pi_Hole_Dia',            2.75, 'Pi mounting hole clearance diameter (mm)'),
        ('Display_Length',        65.0,  'Display module PCB length (mm)'),
        ('Display_Width',         30.5,  'Display module PCB width (mm)'),
        ('Speaker_Square_Size',   32.0,  'Speaker square body footprint (mm)'),
        ('Speaker_Depth',         15.5,  'Speaker total depth — Dayton CE32A-4 (mm)'),
        ('Speaker_Cutout_Dia',    28.0,  'Speaker front grille cutout diameter (mm)'),
        ('Amp_Length',            19.4,  'MAX98357A amp board length (mm)'),
        ('Amp_Width',             17.8,  'MAX98357A amp board width (mm)'),
        ('Amp_Thickness',          3.0,  'MAX98357A board + tallest component stack (mm)'),
        ('Encoder_PCB_Length',    26.0,  'Rotary encoder PCB length (mm)'),
        ('Encoder_PCB_Width',     19.0,  'Rotary encoder PCB width (mm)'),
        ('Encoder_Shaft_Hole',     7.0,  'Encoder shaft panel clearance hole diameter (mm)'),
        ('Wall_Thickness',         2.0,  'FDM outer shell wall thickness (mm)'),
        ('Tolerance',              0.2,  'Bilateral print tolerance / clearance per side (mm)'),
    ]

    created_params = {}
    for name, val_mm, comment in param_defs:
        vi = adsk.core.ValueInput.createByString(f'{val_mm} mm')
        p  = params.add(name, vi, 'mm', comment)
        created_params[name] = p

    # ── 3. Component Hierarchy ───────────────────────────────────────────
    identity = adsk.core.Matrix3D.create()

    def make_comp(parent, name):
        occ = parent.occurrences.addNewComponent(identity)
        occ.component.name = name
        return occ

    make_comp(root, 'Enclosure_Shell')
    make_comp(root, 'Speaker_Chamber')
    hw_occ  = make_comp(root, 'Hardware_Placeholders')
    hw_comp = hw_occ.component

    # ── 4. Placeholder Bounding Boxes ────────────────────────────────────
    def placeholder_box(comp, label, x_offset_mm, len_p, wid_p, ht_p):
        sk  = comp.sketches.add(comp.xYConstructionPlane)
        sk.name = f'{label}_Outline'
        lc, wc = created_params[len_p].value, created_params[wid_p].value
        x0 = x_offset_mm / 10.0   # mm to cm (Fusion internal units)
        sl = sk.sketchCurves.sketchLines
        sl.addByTwoPoints(adsk.core.Point3D.create(x0,    0,  0),
                          adsk.core.Point3D.create(x0+lc, 0,  0))
        sl.addByTwoPoints(adsk.core.Point3D.create(x0+lc, 0,  0),
                          adsk.core.Point3D.create(x0+lc, wc, 0))
        sl.addByTwoPoints(adsk.core.Point3D.create(x0+lc, wc, 0),
                          adsk.core.Point3D.create(x0,    wc, 0))
        sl.addByTwoPoints(adsk.core.Point3D.create(x0,    wc, 0),
                          adsk.core.Point3D.create(x0,    0,  0))
        ext = comp.features.extrudeFeatures.addSimple(
            sk.profiles.item(0),
            adsk.core.ValueInput.createByString(ht_p),
            adsk.fusion.FeatureOperations.NewBodyFeatureOperation)
        ext.bodies.item(0).name = f'{label}_BoundingBox'

    placeholder_box(hw_comp, 'RaspberryPi', 0,
        'Pi_Length', 'Pi_Width', 'Wall_Thickness')

    placeholder_box(hw_comp, 'Speaker', 80,
        'Speaker_Square_Size', 'Speaker_Square_Size', 'Speaker_Depth')

    placeholder_box(hw_comp, 'Amp', 150,
        'Amp_Length', 'Amp_Width', 'Amp_Thickness')

    app.activeViewport.fit()
    print('Setup complete.')
