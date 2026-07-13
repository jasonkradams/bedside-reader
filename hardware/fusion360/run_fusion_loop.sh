#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# Fusion 360 validation loop driver for the bedside-clock enclosure.
#
#   1. runs the hermetic validator (fast, local, no Fusion needed)
#   2. pushes the builder into a running Fusion 360 via the AntigravityBridge
#      add-in (/execute), which rebuilds the model in a fresh document
#   3. frames a camera view (/camera) and grabs a PNG snapshot (/snapshot)
#   4. prints the in-Fusion interference report
#
# Usage:   ./run_fusion_loop.sh [iso|front|top|bottom|back]   (default: iso)
#
# Prereq:  in Fusion 360 -> Utilities -> ADD-INS -> "Scripts and Add-Ins" ->
#          Add-Ins tab -> AntigravityBridge -> Run.  You should see
#          "listening on http://127.0.0.1:8081".  Add it ONCE via the green "+"
#          pointing at hardware/fusion360/AntigravityBridge (only one reference --
#          a second copy will refuse to start to avoid a port conflict).
# ---------------------------------------------------------------------------
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BRIDGE="http://127.0.0.1:8081"
VIEW="${1:-iso}"
OUT="${SNAP_DIR:-$HERE}/snapshot_${VIEW}.png"

echo "== 1. hermetic validator =="
if ! python3 "$HERE/validate_layout.py"; then
  echo ">> layout validation FAILED - fix layout_spec.py before pushing to Fusion." >&2
  exit 1
fi

echo
echo "== 2. bridge check =="
if ! curl -s -m 3 -o /dev/null "$BRIDGE/snapshot"; then
  cat >&2 <<EOF
>> AntigravityBridge is not answering on $BRIDGE.
   Open Fusion 360, then Utilities > ADD-INS > Scripts and Add-Ins > Add-Ins tab,
   select "AntigravityBridge" and click Run (expect a "listening on 8081" popup).
   Then re-run this script.
EOF
  exit 2
fi
echo "bridge is up."

echo
echo "== 3. push build to Fusion (/execute) =="
# bootstrap: reload our modules from disk and rebuild in a clean document
BOOTSTRAP=$(cat <<PY
import sys
d = r"$HERE"
if d not in sys.path:
    sys.path.insert(0, d)
for m in ("layout_spec", "fusion360_setup_v4"):
    sys.modules.pop(m, None)
import fusion360_setup_v4 as B
B.SILENT = True
# close only our own previous outputs; leave any other open documents alone
for doc in list(app.documents):
    try:
        if doc.name.startswith("Bedside_Audiobook_V4"):
            doc.close(False)
    except Exception:
        pass
B.run(None)
PY
)
RESP=$(curl -s -m 120 -X POST --data-binary "$BOOTSTRAP" "$BRIDGE/execute")
echo "execute -> $RESP"
if ! echo "$RESP" | grep -q '"status": *"ok"'; then
  echo ">> build reported an error (see above)." >&2
fi

echo
echo "== 4. camera ($VIEW) + snapshot =="
# model bounds in Fusion cm: X 0..10.8, Y 0..5.4, front Z=0, back Z=-2.7; centre:
CX=5.4; CY=2.7; CZ=-1.35
case "$VIEW" in
  front)  EYE="[$CX,$CY,26]";        UP="[0,1,0]";;
  back)   EYE="[$CX,$CY,-26]";       UP="[0,1,0]";;
  top)    EYE="[$CX,26,$CZ]";        UP="[0,0,-1]";;
  bottom) EYE="[$CX,-24,$CZ]";       UP="[0,0,-1]";;
  iso|*)  EYE="[22,17,23]";          UP="[0,1,0]";;
esac
curl -s -m 15 -X POST "$BRIDGE/camera" \
  -d "{\"eye\":$EYE,\"target\":[$CX,$CY,$CZ],\"up\":$UP,\"isSmoothTransition\":false}" >/dev/null
curl -s -m 15 -X POST "$BRIDGE/camera" -d '{"fit":true}' >/dev/null
if curl -s -m 30 -o "$OUT" "$BRIDGE/snapshot" && [ -s "$OUT" ]; then
  echo "snapshot -> $OUT ($(wc -c < "$OUT") bytes)"
else
  echo ">> failed to grab snapshot" >&2
fi

echo
echo "== 5. in-Fusion interference report =="
if [ -f "$HERE/validation_report_fusion.json" ]; then
  cat "$HERE/validation_report_fusion.json"
else
  echo "(no fusion report written)"
fi
