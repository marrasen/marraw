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
library is empty, so the folder given with -folder shows at its top. The arrow keys, a click, Ctrl and Shift
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

D opens the develop panel beside the photo: the histogram, the main
adjustments and the tone curve. Drag a slider to see the photo change
live; letting go saves the edit, as marraw does. Shift drags finely, a
double click on a slider or a click on its reset mark sends it back. On
the curve, a click adds a point, a drag moves it and a double click
removes it. Edits are saved next to the photos, so try it on these
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
