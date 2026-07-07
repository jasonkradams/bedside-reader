import adsk.core, adsk.fusion, traceback
import threading
import json
import http.server
import urllib.parse
import os
import queue

app = None
ui = None
handlers = []
customEvent = 'AntigravityBridgeEvent'
resultQueue = queue.Queue()
server_thread = None
httpd = None

class BridgeServerHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        parsed_path = urllib.parse.urlparse(self.path)
        if parsed_path.path == '/snapshot':
            self._handle_snapshot()
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        parsed_path = urllib.parse.urlparse(self.path)
        if parsed_path.path == '/camera':
            content_length = int(self.headers.get('Content-Length', 0))
            post_data = self.rfile.read(content_length)
            params = json.loads(post_data.decode('utf-8'))
            self._handle_event('camera', params)
        elif parsed_path.path == '/execute':
            content_length = int(self.headers.get('Content-Length', 0))
            post_data = self.rfile.read(content_length)
            script_content = post_data.decode('utf-8')
            self._handle_event('execute', {'script': script_content})
        else:
            self.send_response(404)
            self.end_headers()

    def _handle_snapshot(self):
        app = adsk.core.Application.get()
        app.fireCustomEvent(customEvent, json.dumps({'action': 'snapshot'}))
        res = resultQueue.get()
        if res.get('status') == 'ok':
            with open(res['path'], 'rb') as f:
                img_data = f.read()
            self.send_response(200)
            self.send_header('Content-Type', 'image/png')
            self.end_headers()
            self.wfile.write(img_data)
        else:
            self.send_response(500)
            self.end_headers()
            self.wfile.write(res.get('error', '').encode('utf-8'))

    def _handle_event(self, action, params):
        app = adsk.core.Application.get()
        payload = {'action': action}
        payload.update(params)
        app.fireCustomEvent(customEvent, json.dumps(payload))
        res = resultQueue.get()
        self.send_response(200 if res.get('status') == 'ok' else 500)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(res).encode('utf-8'))

    def log_message(self, format, *args):
        # Silence default http logs
        pass

class ThreadedHTTPServer(threading.Thread):
    def __init__(self):
        threading.Thread.__init__(self)
        self.daemon = True

    def run(self):
        global httpd
        server_address = ('127.0.0.1', 8081)
        httpd = http.server.HTTPServer(server_address, BridgeServerHandler)
        httpd.serve_forever()

class BridgeEventHandler(adsk.core.CustomEventHandler):
    def __init__(self):
        super().__init__()

    def notify(self, args):
        try:
            payload = json.loads(args.additionalInfo)
            action = payload.get('action')
            res = {'status': 'error', 'error': 'Unknown action'}
            
            if action == 'snapshot':
                vp = app.activeViewport
                # Save into the same directory as this script
                path = os.path.join(os.path.dirname(__file__), 'snapshot.png')
                if vp.saveAsImageFile(path, 1920, 1080):
                    res = {'status': 'ok', 'path': path}
                else:
                    res = {'status': 'error', 'error': 'Failed to save snapshot'}
            
            elif action == 'camera':
                vp = app.activeViewport
                cam = vp.camera
                
                if 'eye' in payload:
                    e = payload['eye']
                    cam.eye = adsk.core.Point3D.create(e[0], e[1], e[2])
                if 'target' in payload:
                    t = payload['target']
                    cam.target = adsk.core.Point3D.create(t[0], t[1], t[2])
                if 'up' in payload:
                    u = payload['up']
                    cam.upVector = adsk.core.Vector3D.create(u[0], u[1], u[2])
                if 'isSmoothTransition' in payload:
                    cam.isSmoothTransition = payload['isSmoothTransition']
                
                if payload.get('fit'):
                    vp.fit()
                else:
                    vp.camera = cam
                    
                adsk.doEvents()
                res = {'status': 'ok'}
            
            elif action == 'execute':
                script = payload.get('script')
                exec_globals = {'adsk': adsk, 'app': app, 'ui': ui}
                exec(script, exec_globals)
                res = {'status': 'ok'}

            resultQueue.put(res)

        except Exception as e:
            resultQueue.put({'status': 'error', 'error': str(e) + '\n' + traceback.format_exc()})

def run(context):
    global app, ui, server_thread, handlers
    try:
        app = adsk.core.Application.get()
        ui  = app.userInterface
        
        try:
            app.unregisterCustomEvent(customEvent)
        except:
            pass
        
        event = app.registerCustomEvent(customEvent)
        onCustomEvent = BridgeEventHandler()
        event.add(onCustomEvent)
        handlers.append(onCustomEvent)
        
        server_thread = ThreadedHTTPServer()
        server_thread.start()
        
        ui.messageBox('Antigravity Bridge Add-in started!\nListening on http://127.0.0.1:8081')

    except Exception as e:
        if ui:
            ui.messageBox('Failed:\n{}'.format(traceback.format_exc()))

def stop(context):
    try:
        global httpd, handlers
        if httpd:
            # We can't cleanly shutdown from this thread without blocking if it's not careful, 
            # but setting the socket to close usually breaks the loop.
            httpd.server_close()
        
        app = adsk.core.Application.get()
        app.unregisterCustomEvent(customEvent)
        handlers = []
        
        ui = app.userInterface
        ui.messageBox('Antigravity Bridge Add-in stopped.')
    except Exception as e:
        pass
