marraw gunim test kit
=====================

A test build of marraw's cull view drawn with gunim, the RAW photos to try
it on, and a script that measures it. It needs nothing installed: the
program runs marraw's backend inside itself, with a data folder of its own.

What's here
-----------

  bin\marraw-cull.exe          the test build, with its own backend
  bin\marraw-cull-connect.exe  the same, connecting to another marraw only
  bin\onnxruntime*.dll         the AI runtime (not used by the cull view)
  photos\...Canon CR2\         40 photos, 18 MP, daylight
  photos\...Sony ARW\          20 photos, large files, a concert
  run-tests.ps1                the measurements

1. The measurements (about 10 minutes, hands off)
-------------------------------------------------

In PowerShell, in this folder:

    powershell -ExecutionPolicy Bypass -File .\run-tests.ps1

A window opens and steps through each folder by itself, three times: fast
through photos never seen, slower, and fast again once they are rendered.
Windows may ask about an unknown publisher the first time: More info, then
Run anyway. When it is done, send results.zip.

2. By hand
----------

    .\bin\marraw-cull.exe -folder ".\photos\2017-03-24 Flygtur (Canon CR2)" -data-dir $env:TEMP\marraw-cull-test-data

It opens on the folder's grid, with the library in a sidebar on the
left: a click on a shoot there opens it. With the test data folder the
library is empty, so the folder given with -folder shows at its top.
To fill it, add this kit's photos folder once, and its shoots appear:

    .\bin\marraw-cull.exe -add-library .\photos -data-dir $env:TEMP\marraw-cull-test-data

The arrow keys, a click, Ctrl and Shift
select; Ctrl and the wheel change the tiles' size. 0 to 5 rate the photos
selected, P picks, X rejects and U clears the flag, as in marraw. Enter
or a double click opens a photo in the cull view.

In the cull view, Left and Right step through the folder, Home and End
go to its ends, and a click on the filmstrip goes to that photo. The
same keys rate and flag it. Z, Space or a double click goes between fit
and 100%, + and - zoom in steps, the wheel zooms about the pointer, and
dragging pans, a flick glides and Shift with the arrows pans too; the
zoom stays as you step, to compare a burst. Escape
goes back to the grid. The corner says which rendition shows, how long
it took, and how the full-resolution tiles stand.

D opens the develop panel beside the photo: the histogram, then
sections that fold open and shut with a click on their heading, a dot
showing which hold changes: tone, presence, white balance (As shot, Auto
or Kelvin), colour (black and white, split toning), the colour mixer,
effects, detail and the tone curve. Auto beside Tone and Color sets them
for the photo. With the panel open, Up and Down walk its controls, + and
- step the one chosen (Shift for big steps), a letter jumps to one as in
marraw (E exposure, C contrast, T temperature, ...), and Escape lets it go. Drag a slider to see the photo change
live; letting go saves the edit, as marraw does. Shift drags finely, a
double click on a slider or a click on its reset mark sends it back. On
the curve, a click adds a point, a drag moves it and a double click
removes it. Ctrl+C copies a photo's edit settings and Ctrl+V pastes them,
in the grid on every photo selected. Ctrl+Z undoes a change and Ctrl+Shift+Z or Ctrl+Y redoes
it, each photo with its own history. Edits are saved next to the photos, so try it on these
copies only.

Every action has a short animation: the tiles growing in, the photo
flying out of its tile and back, stars and flags changing. Tell me if
any of them feels slow or gets in the way.
Worth noticing:

  - Does stepping ever freeze or stutter, even for a moment?
  - How soon does a photo turn sharp once you stop on it?
  - Holding Right down: does the window keep up?
  - Zoomed in: does panning and zooming stay smooth, and the step to the
    next photo too?

What to send back
-----------------

  - results.zip
  - a sentence or two on how skimming by hand felt.
