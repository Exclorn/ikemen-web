# Ikemen GO in the browser (WebAssembly + WebGL2)

Build (needs the Go version in go.mod, the screenpack checkout and `zip`):

    web/build.sh /path/to/Ikemen_GO-Screenpack
    cd web/dist && python3 -m http.server 8080   # open http://localhost:8080

Status: the main package has NOT been compiled together yet. Verified separately: glw (WebGL2 binding, tested in
Chromium with the engine's sprite shaders), fs.js (tested against Go's os package), zip.js and the packaging.
Expect compile errors on the first real build.

Limitations: no 3D models/shadows, no video, no tracker music (xm/mod), no netplay, no system font search.
Added characters/stages must be listed in data/select.def. Keyboard bindings use physical key positions.
Saves (save/) persist in IndexedDB.
