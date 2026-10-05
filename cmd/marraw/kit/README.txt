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

Left and Right (or Space and Backspace) step through the folder, Home and
End go to its ends, Escape quits. Z or a double click goes between fit
and 100%, the wheel zooms about the pointer, and dragging pans; the zoom
stays as you step, to compare a burst. The corner says which rendition
shows, how long it took, and how the full-resolution tiles stand.
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
