# Fusion 360 Enclosure Design Guide

Don't worry, the script did exactly what it was supposed to do! It didn't build the final enclosure for you; instead, it built the **"Bounding Boxes"** (the three grey blocks you see). 

Think of these blocks as strict physical "no-fly zones". Because you know exactly how big the Pi, the Amplifier, and the Speaker are, these blocks represent the absolute minimum physical space you must design around. If your plastic enclosure cuts into these blocks, your real-world electronics won't fit!

Here is the step-by-step workflow for turning those floating blocks into a beautiful, 3D-printable enclosure:

## Phase 1: The Layout (Arranging the Guts)
Right now, the boxes are sitting flat on the origin. You need to arrange them into the physical layout of your clock.
1. Right-click the **Hardware_Placeholders** component in your browser and make sure it is activated.
2. Press **`M` (Move/Copy)**.
3. Select the Speaker block and move it to where you want the speaker to be (e.g., facing forward or downward).
4. Move the Pi block to where the screen will be (remember, the screen sits directly on top of the Pi block).
5. Move the Amplifier block to an empty spot in the back.
*Tip: Leave at least 2mm of space between all blocks for wiring and airflow!*

## Phase 2: The "Clay Block" (Top-Down Design)
Instead of trying to draw complex interlocking walls right away, we use a technique called "Top-Down Design". You will draw one massive solid block of plastic, and then hollow it out.
1. Create a **New Component** and call it `Enclosure_Shell`.
2. Create a sketch on the floor plane and draw a large rectangle (or circle) that completely swallows all of your hardware blocks.
3. **Extrude (`E`)** that sketch upward until every single hardware block is completely encased inside this new giant solid block.

## Phase 3: Making it Look Good (Fillets)
Since you want a nice, ergonomic feel without sharp corners:
1. Hide your `Hardware_Placeholders` component for a moment so you just see your solid block.
2. Press **`F` (Fillet)**.
3. Select all the sharp outer edges of your solid block and drag the arrow inward (e.g., 10mm or 15mm). This will turn your sharp box into a smooth, pebble-like shape.

## Phase 4: Hollowing it Out (The Shell Tool)
Now we need to make room for the electronics inside.
1. Select the **Shell** tool from the Modify menu.
2. Select the bottom face of your solid block (if you want the bottom to be open) or select the entire body.
3. Set the Inside Thickness to your parameter: `Wall_Thickness` (usually 2mm or 2.5mm is great for 3D printing). 
Fusion 360 will instantly hollow out the entire inside of your pebble, leaving a perfect 2mm thick shell!

## Phase 5: Splitting into Multiple Pieces
You can't 3D print a closed hollow ball and put electronics in it; it needs to open up.
1. Use the **Split Body** tool.
2. Select your hollowed-out enclosure as the "Body to Split".
3. Use a construction plane (like the XY plane) or draw a simple line sketch to act as the "Splitting Tool". 
4. This will slice your enclosure horizontally into a "Top Half" and a "Bottom Half". 

## Phase 6: Adding the Interlocks (Snap Fits)
To make the top and bottom halves clip together:
1. Hide the Top Half. 
2. On the rim of the Bottom Half, use the **Extrude** tool to pull up a small 1mm thick inner "Lip" that goes all the way around the edge.
3. Make this lip slightly smaller than the outer wall (this is where your `Tolerance` parameter comes in, usually 0.15mm gap).
4. Unhide the Top Half, and use the **Combine** tool (set to "Cut") using the Bottom Half as the tool body. This will carve a perfect receiving groove into the Top Half!

## Phase 7: Cutouts and Mounts
Finally, unhide your `Hardware_Placeholders`.
1. See where the Pi block intersects the front wall? Cut a square hole there for the screen.
2. See where the Speaker block sits? Cut a grill pattern in the plastic right in front of it.
3. Draw small cylinders (standoffs) coming up from the floor to meet the screw holes on the Pi block.

By following this workflow, your enclosure will always perfectly wrap around your electronics, ensuring a flawless fit on the very first print!

For the detailed component layout, button mappings, and dimensions, refer to the [Enclosure Design Specification](../design_specs/enclosure_design_spec.md).

