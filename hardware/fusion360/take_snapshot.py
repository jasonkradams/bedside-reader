import adsk.core, adsk.fusion, traceback
import os

def run(context):
    ui = None
    try:
        app = adsk.core.Application.get()
        ui  = app.userInterface
        viewport = app.activeViewport
        
        # Define the path to save the image
        # Saving it directly to the project directory so the AI can easily find and view it
        output_path = '/Users/jasonadams/code/github/jasonkradams/bedside-clock/hardware/fusion360/snapshot.png'
        
        # Save the image (1920x1080 resolution)
        success = viewport.saveAsImageFile(output_path, 1920, 1080)
        
        if success:
            ui.messageBox(f'Snapshot saved successfully to:\n{output_path}')
        else:
            ui.messageBox('Failed to save snapshot.')

    except Exception:
        if ui:
            ui.messageBox('Failed:\n{}'.format(traceback.format_exc()))
