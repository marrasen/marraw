package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary between the window and the culler.
type (
	// Cull is what the window shows: the photo at Index of Total, the best
	// pixels of it so far, and what they are.
	Cull struct {
		Index, Total int
		ID           int64
		Name         string
		// Exif is the photo's camera and exposure, in a line, for the
		// panel's header.
		Exif string
		// Errors are the errors the user has not cleared, and Tasks the
		// background tasks under way.
		Errors []ErrorNote
		Tasks  []TaskNote
		Img    *paint.Image
		// Thumb is the photo's small picture, to show until Img comes.
		Thumb *paint.Image
		// Aspect is the photo's width over its height, for its frame before
		// any pixels arrive.
		Aspect float32
		// Note says which rendition shows and what it took.
		Note string
		// Full is the photo's full resolution, which the tiles cover, and
		// Tiles those of them fetched, by their place in the grid.
		Full  image.Point
		Tiles map[image.Point]*paint.Image
		// TileNote says how the tiles stand.
		TileNote string
		// Panel says the develop panel is open beside the photo, and Live
		// that Img is a live preview of an edit under way.
		Panel bool
		Live  bool
		// Active is the develop control the keys act on, or none, and
		// Original says the photo shows as it was before any edit.
		Active   string
		Original bool
		// WBPick says the white-balance eyedropper is out, and WBFrame
		// and WBPix are the frame it samples, for its magnifier, once
		// they come.
		WBPick  bool
		WBFrame *paint.Image
		WBPix   *image.RGBA
		// Notice is a note to show over the photo for a moment, each time
		// NoticeSeq changes, as an undo says what it undid.
		Notice    string
		NoticeSeq int
		// Aids are the photo's culling aids.
		Aids Aids
		// Groups are the time-gap groups the filmstrip shows of, and
		// GroupCount how many the folder has, none where it is not grouped.
		Groups     []StripGroup
		GroupCount int
		// Crop is the crop under way, or nil, and Masks the masks being
		// worked on, or nil.
		Crop  *CropView
		Masks *MaskView
		// Rating and Flag are the photo's, and Strip the photos around it,
		// for the filmstrip.
		Rating int
		Flag   string
		Strip  []Thumb
	}
	// Thumb is one photo in the filmstrip: where it is in the folder, its
	// small picture once fetched, and its rating and flag.
	Thumb struct {
		Index  int
		Img    *paint.Image
		Aspect float32
		Rating int
		Flag   string
		Aids   Aids
		// GapBefore is the minutes since the group before, where the
		// photo starts a group of the grid's, or below nought.
		GapBefore int
	}
	// Rate rates the photo showing, 0 to 5 stars.
	// With ID it rates that photo alone, as a click on its stars does,
	// and Toggle takes a rating it has already off again.
	Rate struct {
		Stars  int
		ID     int64
		Toggle bool
	}
	// Mark flags the photo showing: "pick", "exclude" or "none"; with ID,
	// that photo alone.
	Mark struct {
		Flag string
		ID   int64
	}
	// WantTiles asks for the tiles of the photo at Index in Range of the
	// grid, as the view zooms past what the 2048 shows sharp; an empty
	// Range wants none.
	WantTiles struct {
		Index int
		Range image.Rectangle
	}
	// Step moves through the folder by By photos.
	Step struct{ By int }
	// Jump goes to the photo at To, or, negative, to the last.
	Jump struct{ To int }
	// Quit closes the window.
	Quit struct{}
)

// dwell is how long a photo has to stay before a cold one gets a real
// render, as the React loupe waits: skimming past never starts a RAW
// decode.
const dwell = 350 * time.Millisecond

// culler is the application half: the folder's photos, where the user is,
// and the pipeline that brings each photo's pixels in without ever making
// navigation wait on a RAW decode.
type culler struct {
	ctx        context.Context
	c          gunim.Client
	api        *marrawclient.Client
	im         *images
	folder     int64
	folderPath string
	photos     []marrawclient.Photo
	// index is where each photo is in photos, by its ID, and aspects the
	// shapes learned from their pixels.
	index   map[int64]int
	aspects map[int64]float32
	// all is the folder's every photo, and allIndex where each is in it;
	// photos are those of them libView shows, in its order, and viewSeq
	// counts the views made. ui is marraw's settings, as last read.
	all      []marrawclient.Photo
	allIndex map[int64]int
	libView  LibView
	viewSeq  int
	ui       *marrawclient.UISettings
	at       int

	// cache holds decoded pixels by photo, the best of each so far.
	cache *pixelCache
	// load cancels the current photo's pipeline, and warm the neighbours'.
	load, warm context.CancelFunc
	// gen counts navigations, so a late result for a photo left behind is
	// dropped.
	gen int
	// arrived carries the pipelines' results to the culler's goroutine,
	// and do work for it from elsewhere.
	arrived chan arrival
	do      chan func()
	// stepped is when the user last moved, for the timings.
	stepped time.Time
	timings []timing

	// tiles holds the photos' full-resolution tiles, and tilesFor the state
	// of the photo showing's.
	tiles    *tileCache
	tileWant WantTiles
	tileWarm map[string]bool
	// tileStop cancels the tile fetches under way; probing is the photo
	// whose tiles are being looked for, or rendered, and probeStop stops
	// that.
	tileStop  context.CancelFunc
	probing   int64
	probeStop context.CancelFunc
	tileNote  string
	// errs are the errors the user has not cleared, and errSeq the last
	// one's number.
	errs   []ErrorNote
	errSeq int
	// presetAmt is the preset applied last, for its Amount, and
	// amountScrub says the Amount is making the edit, which keeps it.
	presetAmt   *presetAmount
	amountScrub bool
	// tasks are the backend's tasks, for the tray, and taskOf the task
	// each subtask showing is under.
	tasks  map[string]*taskRun
	taskOf map[string]string

	// thumbs are the small pictures of the grid and the filmstrip by
	// photo, the last thumbKeep of them, and thumbsWanted those asked for
	// and not in yet.
	thumbs       map[int64]*paint.Image
	thumbOrder   []int64
	thumbsWanted map[int64]bool
	thumbSlots   chan struct{}
	// gridStop cancels the grid's fetches when it scrolls on.
	gridStop context.CancelFunc

	// culling says the cull view is open over the grid; sel and cursor are
	// the grid's selection.
	culling bool
	sel     [][2]int
	cursor  int

	// dev is the develop panel's side.
	dev developer

	// cullSteps are the ratings and flags given, to undo, and cullAt how
	// many of them stand.
	cullSteps []cullStep
	cullAt    int
	// wb is the white-balance eyedropper, while it is out.
	wb wbPick
	// aids are the folder's culling aids: its bursts, and what is soft.
	aids aidsOf
	// presetGen counts the presets laid or shown, so a late one is let go.
	presetGen int
	// masks are the develop panel's masks: the one chosen, the brush; and
	// devTab is the panel's tab showing.
	masks  maskState
	devTab int
	// crop is cropping, while it goes on, and presetsShown says the
	// panel's presets show, for their small pictures to render.
	crop         cropMode
	presetsShown bool
	// original says the photo shows as it was before any edit, origImg
	// those pixels for photo origID, and swapNow that the next picture
	// shows at once, with no fade.
	original bool
	origID   int64
	origImg  *paint.Image
	swapNow  bool
	// asking says a dialog is open, and deleting holds the photos its
	// answer deletes.
	asking   bool
	deleting []int64
	// exporting holds the photos the export dialog is for, and exports
	// the exports under way, by their tasks.
	exporting []int64
	exports   map[string]exportRun
	// scans are the analyses under way: eyes, subjects.
	scans map[string]scanRun
	// clipboard is the edit copied, to paste.
	clipboard *marrawclient.Params
	// notice is a note over the photo, shown anew as noticeSeq changes.
	notice    string
	noticeSeq int

	// stopLive stops following the folder showing, and rail is the
	// library as the sidebar shows it.
	stopLive func()
	rail     RailState
}

// stripReach is how many photos the filmstrip shows on each side of the
// one showing.
const stripReach = 80

// arrival is a rendition decoded for photo at index, in generation gen.
type arrival struct {
	id         int64
	gen, index int
	rank       int
	want       want
	got        got
}

// timing is how long one step took to show pixels, and to show sharp ones;
// sharpSeen says it did, as at once from the cache, where sharp is 0.
type timing struct {
	first, sharp time.Duration
	firstWhat    string
	sharpSeen    bool
}

func newCuller(ctx context.Context, c gunim.Client, api *marrawclient.Client, im *images, folder int64, folderPath string, photos []marrawclient.Photo) *culler {
	cu := &culler{aspects: map[int64]float32{}, ctx: ctx, c: c, api: api, im: im, folder: folder, folderPath: folderPath,
		cache: newPixelCache(16), arrived: make(chan arrival, 16), do: make(chan func(), 16),
		tiles: newTileCache(48), tileWarm: map[string]bool{},
		thumbs: map[int64]*paint.Image{}, thumbsWanted: map[int64]bool{}, thumbSlots: make(chan struct{}, 6), cursor: -1,
		libView: defaultView("", defaultGap), masks: maskState{sel: -1, brush: defaultBrush, hover: -1, tintOf: -1}}
	cu.setAll(photos)
	cu.applyView()
	return cu
}

func (cu *culler) serve() error {
	_ = cu.c.SetTheme("marraw")
	// The library grid is always there; the cull view opens over it.
	if err := cu.c.Mount(gunim.Root, "grid", "grid", cu.gridState(), "grid"); err != nil {
		return err
	}
	_ = cu.c.Focus("grid")
	// The folder's photos as a live query, as the Electron app holds
	// them: ratings, flags and edits, made here or elsewhere on the same
	// backend, come as patches, and each new listing brings what the
	// backend has measured since, such as sizes and base exposures.
	if cu.folder != 0 {
		cu.stopLive = cu.followFolder()
	}
	// The backend's tasks as they change, for the exports' notes.
	cu.exports = map[string]exportRun{}
	cu.scans = map[string]scanRun{}
	stopTasks := cu.api.OnTaskStateEvent(func(ev marrawclient.TaskStateEvent) {
		select {
		case cu.do <- func() { cu.tasksChanged(ev.Tasks) }:
		case <-cu.ctx.Done():
		}
	})
	defer stopTasks()
	// And how far each has got, which comes apart from its state.
	stopProgress := cu.api.OnTaskUpdateEvent(func(ev marrawclient.TaskUpdateEvent) {
		if ev.Current == nil || ev.Total == nil {
			return
		}
		select {
		case cu.do <- func() { cu.taskProgress(ev.TaskID, *ev.Current, *ev.Total) }:
		case <-cu.ctx.Done():
		}
	})
	defer stopProgress()
	go cu.loadSettings()
	defer func() {
		if cu.stopLive != nil {
			cu.stopLive()
		}
	}()
	go cu.loadLibrary()
	for {
		select {
		case <-cu.ctx.Done():
			return nil
		case a := <-cu.arrived:
			cu.take(a)
		case fn := <-cu.do:
			fn()
		case ev, ok := <-cu.c.Intents():
			if !ok {
				return cu.c.Err()
			}
			switch in := ev.Intent.(type) {
			case Step:
				cu.goTo(cu.at + in.By)
			case Jump:
				to := in.To
				if to < 0 {
					to = len(cu.photos) - 1
				}
				cu.goTo(to)
			case WantTiles:
				cu.wantTiles(in)
			case Rate:
				cu.rate(in)
			case Mark:
				cu.mark(marrawclient.Flag(in.Flag), in.ID)
			case NeedThumbs:
				cu.needThumbs(in)
			case Selected:
				cu.sel, cu.cursor = in.Runs, in.Cursor
			case OpenCull:
				cu.openCull(in.Index)
			case LeaveCull:
				cu.leaveCull()
			case ToggleDevelop:
				cu.toggleDevelop()
			case DevSet:
				cu.devSet(in)
			case DevCurve:
				cu.devCurve(in)
			case DevChannel:
				cu.devChannel(in.Channel)
			case DevUndo:
				cu.undo(in.Redo)
			case DevChoice:
				cu.devChoose(in)
			case DevWalk:
				cu.devWalk(in.By)
			case DevNudge:
				cu.devNudge(in)
			case DevPick:
				if in.Key != "" && cu.devTab != tabDevelop {
					cu.showTab(tabDevelop)
				}
				cu.setActive(in.Key)
			case DevTab:
				if in.Open && !cu.dev.open {
					cu.toggleDevelop()
				}
				if in.By != 0 {
					cu.showTab((cu.devTab + in.By + len(devTabs)) % len(devTabs))
				} else {
					cu.showTab(in.Index)
				}
			case DevAuto:
				cu.devAuto(in)
			case MaskMove:
				cu.maskMove(in)
			case PresetAmount:
				cu.setPresetAmount(in)
			case ErrClear:
				cu.clearError(in)
			case TaskCancel:
				cu.cancelTask(in.ID)
			case Notify:
				cu.notify(in.Text)
			case InfoLocate:
				cu.locate()
			case EditCopy:
				cu.editCopy()
			case EditPaste:
				cu.editPaste()
			case OpenShoot:
				cu.openShoot(in.Path)
			case SetLibView:
				cu.setView(in.View)
			case AskDelete:
				cu.askDelete()
			case ShowOriginal:
				cu.showOriginal(in.On)
			case DevReset:
				cu.devReset()
			case AskExport:
				cu.askExport()
			case ShowShortcuts:
				cu.showShortcuts()
			case ExportGo:
				cu.exportGo(in)
			case DevJump:
				cu.devJump(in.Index)
			case DevWBPick:
				cu.devWBPick(in.On)
			case DevWBBar:
				cu.devWBBar(in.Act)
			case MaskAdd:
				cu.maskAdd(in.Kind)
			case MaskAI:
				cu.maskAI(in)
			case MaskSelect:
				cu.maskSelect(in.Index)
			case MaskSet:
				cu.maskSet(in)
			case MaskFlag:
				cu.maskFlag(in)
			case MaskDelete:
				cu.maskDelete(in.Index)
			case MaskGeom:
				cu.maskGeom(in)
			case BrushSet:
				cu.masks.brush = in.Tool
				cu.masksChanged()
			case BrushClear:
				cu.brushClear(in.Index)
			case RangePick:
				cu.masks.rangePick = in.On && cu.maskOK(cu.masks.sel)
				cu.masks.brush.Painting = false
				cu.masksChanged()
			case RangeAt:
				cu.rangeAt(in.X, in.Y)
			case MaskHover:
				cu.maskHover(in.Index)
			case MaskEscape:
				cu.masksEscape()
			case ToggleCrop:
				cu.toggleCrop()
			case CropDone:
				cu.cropDone()
			case CropSet:
				cu.cropSet(in.Rect)
			case CropAngle:
				cu.cropAngle(in)
			case CropTurn:
				cu.cropTurn(in)
			case CropFlip:
				cu.cropFlip(in)
			case CropAspect:
				cu.cropAspect(in.Index)
			case CropAuto:
				cu.cropAuto(in.Download)
			case CropReset:
				cu.cropReset()
			case PresetApply:
				cu.presetApply(in)
			case PresetHover:
				cu.presetHover(in)
			case PresetsShown:
				cu.presetsShown = in.On
				cu.loadPresetThumbs()
			case AskPreset:
				cu.askPreset()
			case PresetSave:
				cu.presetSave(in)
			case PresetDelete:
				cu.presetDelete(in.Index)
			case BurstKeep:
				cu.burstKeep(in.Pick)
			case JudgeBursts:
				cu.judgeBursts()
			case CheckEyes:
				cu.checkEyes(in.Download)
			case CheckSubjects:
				cu.checkSubjects(in.Download)
			case DevWBAt:
				cu.devWBAt(in.X, in.Y)
			case Confirmed:
				cu.confirmed(in)
			case Quit:
				cu.c.Leave()
			}
		}
	}
}

// state is what the window shows now.
func (cu *culler) state() Cull {
	p := cu.photos[cu.at]
	st := Cull{Index: cu.at, Total: len(cu.photos), ID: p.ID, Name: p.FileName, Exif: exifLine(p), Errors: cu.errs, Tasks: cu.shownTasks(), Aspect: cu.aspectOf(p), Full: cu.fullOf(p),
		Tiles: cu.tilesShowing(p), TileNote: cu.tileNote, Rating: p.Rating, Flag: string(p.Flag), Aids: cu.aids.of(p)}
	gapAt := map[int]int{}
	groups := gapGroups(cu.photos, cu.libView.Gap, cu.libView.Sort)
	days := spansDays(groups)
	lo, hi := max(0, cu.at-stripReach), min(len(cu.photos)-1, cu.at+stripReach)
	for k, g := range groups {
		if k > 0 {
			gapAt[g.Start] = g.GapBefore
		}
		if g.Start+g.Count-1 >= lo && g.Start <= hi {
			sg := StripGroup{Start: g.Start, Count: g.Count, Label: rangeLabel(g, days)}
			if g.GapBefore >= 0 {
				sg.Gap = gapLabel(g.GapBefore, cu.libView.Sort == "captureDesc")
			}
			st.Groups = append(st.Groups, sg)
		}
	}
	st.GroupCount = len(groups)
	for i := max(0, cu.at-stripReach); i <= min(len(cu.photos)-1, cu.at+stripReach); i++ {
		q := cu.photos[i]
		gap, ok := gapAt[i]
		if !ok {
			gap = -1
		}
		st.Strip = append(st.Strip, Thumb{Index: i, Img: cu.thumbs[q.ID], Aspect: cu.aspectOf(q), Rating: q.Rating, Flag: string(q.Flag),
			Aids: cu.aids.of(q), GapBefore: gap})
	}
	if e, ok := cu.cache.get(p.ID); ok {
		st.Img, st.Note = e.img, e.note
	}
	// The edit being made shows as its previews come.
	if d := &cu.dev; d.open && d.live != nil && d.id == p.ID {
		st.Img, st.Note, st.Live = d.live, d.note, true
	}
	// Backspace held: the photo before any edit, and back again, at once.
	st.Original, st.WBPick = cu.original, cu.wb.on
	if d := &cu.dev; d.open && d.id == p.ID {
		st.Masks = cu.maskView()
	}
	if st.Crop = cu.cropView(); st.Crop != nil && st.Crop.Ready && cu.crop.frame.X > 0 {
		// The whole frame shows, in its own shape.
		st.Aspect = float32(cu.frameAspect())
		st.Full = st.Crop.Frame
		if st.Full.X <= 0 || st.Full.Y <= 0 {
			st.Full = cu.crop.frame
		}
	}
	st.WBFrame, st.WBPix = cu.wb.frame, cu.wb.pix
	if img := cu.originalShowing(p.ID); img != nil {
		// The edit's tiles are not the original's.
		st.Img, st.Note, st.Tiles = img, "original, before any edit", nil
	}
	if cu.swapNow {
		st.Live, cu.swapNow = true, false
	}
	st.Thumb = cu.thumbs[p.ID]
	st.Panel = cu.dev.open
	if cu.dev.open {
		st.Active = cu.dev.active
	}
	st.Notice, st.NoticeSeq = cu.notice, cu.noticeSeq
	return st
}

// goTo moves to the photo at i: what is cached shows at once, the pipeline
// for the rest starts, and the old one stops.
func (cu *culler) goTo(i int) {
	i = max(0, min(i, len(cu.photos)-1))
	if !cu.culling || i == cu.at && cu.load != nil {
		return
	}
	// The eyedropper goes, keeping what it picked, and cropping ends.
	cu.wbFinish(true)
	cu.cropDone()
	cu.at, cu.gen, cu.stepped = i, cu.gen+1, time.Now()
	if cu.load != nil {
		cu.load()
	}
	if cu.warm != nil {
		cu.warm()
	}
	if cu.tileStop != nil {
		cu.tileStop()
		cu.tileStop = nil
	}
	if cu.probeStop != nil {
		cu.probeStop()
		cu.probing, cu.probeStop = 0, nil
	}
	cu.tileNote = ""
	cu.original = false
	cu.showCull()
	cu.developFollows(i)
	// The grid follows, so the photo flies back to its own tile.
	cu.sel, cu.cursor = [][2]int{{i, i + 1}}, i
	_ = cu.c.Patch("grid", GridAt{Folder: cu.folder, Index: i})
	cu.loadStrip()
	p := cu.photos[i]
	// Where the user is, so the backend's pre-render works outward from
	// here.
	go func(folder, id int64) { _ = cu.api.Library.SetFocus(cu.ctx, folder, id) }(cu.folder, p.ID)
	if e, ok := cu.cache.get(p.ID); ok && e.rank >= rankSharp {
		cu.record(e.note, true)
		// Here, on the culler's goroutine, at once: warmNeighbours would
		// wait for this goroutine to take it, forever if do is full.
		cu.startWarming()
		return
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.load = cancel
	go cu.pipeline(ctx, cu.gen, i, p)
}

// How good a rendition is: a stand-in at 512, a provisional at the fit
// level (the camera's embedded JPEG or a downscale), or the real pixels.
const (
	rankBase = iota + 1
	rankProvisional
	rankSharp
)

// target is the level the window is shown at.
const target = "2048"

// pipeline brings photo p's pixels in, best last, never asking for a RAW
// decode until the user has stayed on it for dwell.
func (cu *culler) pipeline(ctx context.Context, gen, i int, p marrawclient.Photo) {
	have := 0
	if e, ok := cu.cache.get(p.ID); ok {
		have = e.rank
	}
	try := func(w want, rank int) bool {
		if rank <= have {
			return false
		}
		g, err := cu.im.get(ctx, p, w)
		if err != nil {
			return false
		}
		if g.provisional && rank == rankSharp {
			rank = rankProvisional
		}
		cu.send(arrival{id: p.ID, gen: gen, index: i, rank: rank, want: w, got: g})
		have = max(have, rank)
		return rank == rankSharp
	}
	// Something at once: whatever 512 there is, at any edit state.
	if have == 0 {
		try(want{level: "512", stale: true, fast: true}, rankBase)
	}
	// The real pixels, if they are rendered already.
	if try(want{level: target, cacheOnly: true}, rankSharp) {
		cu.warmNeighbours()
		return
	}
	// The camera's embedded JPEG, or a downscale: tens of milliseconds.
	try(want{level: target, fast: true}, rankSharp)
	if have >= rankSharp {
		cu.warmNeighbours()
		return
	}
	cu.warmNeighbours()
	// Still here: one real render.
	select {
	case <-ctx.Done():
		return
	case <-time.After(dwell):
	}
	try(want{level: target}, rankSharp)
}

// warmNeighbours fetches the next, the previous and the one after next,
// from what is rendered already, so stepping to them is instant. It is for
// the pipelines' goroutines; the culler's own calls startWarming.
func (cu *culler) warmNeighbours() {
	select {
	case cu.do <- cu.startWarming:
	case <-cu.ctx.Done():
	}
}

// send hands a to the culler's goroutine.
func (cu *culler) send(a arrival) {
	select {
	case cu.arrived <- a:
	case <-cu.ctx.Done():
	}
}

// take keeps an arrival, and shows it if it is for the photo showing.
func (cu *culler) take(a arrival) {
	// By its photo, wherever that is now: the folder may have changed.
	i, ok := cu.index[a.id]
	if !ok {
		return
	}
	a.index = i
	p := cu.photos[a.index]
	note := fmt.Sprintf("%s · %dx%d · fetch %d ms, decode %d ms", a.want, a.got.w, a.got.h,
		a.got.fetch.Milliseconds(), a.got.decode.Milliseconds())
	if a.got.provisional {
		note += " · provisional"
	}
	cu.cache.put(p.ID, cacheEntry{img: a.got.img, rank: a.rank, note: note})
	cu.learnShape(p.ID, a.got.w, a.got.h)
	if a.index != cu.at {
		return
	}
	cu.showCull()
	if cu.dev.live == nil {
		cu.histogramShowing()
	}
	if a.gen == cu.gen {
		cu.record(note, a.rank >= rankSharp)
	}
}

// startWarming warms the neighbours of the photo showing, once.
func (cu *culler) startWarming() {
	if cu.warm != nil {
		cu.warm()
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.warm = cancel
	gen := cu.gen
	var idx []int
	for _, d := range []int{1, -1, 2} {
		if j := cu.at + d; j >= 0 && j < len(cu.photos) {
			if e, ok := cu.cache.get(cu.photos[j].ID); !ok || e.rank < rankSharp {
				idx = append(idx, j)
			}
		}
	}
	go func() {
		for _, j := range idx {
			p := cu.photos[j]
			for _, w := range []want{{level: target, cacheOnly: true}, {level: target, fast: true}} {
				g, err := cu.im.get(ctx, p, w)
				if err != nil {
					continue
				}
				rank := rankSharp
				if g.provisional {
					rank = rankProvisional
				}
				cu.send(arrival{id: p.ID, gen: gen, index: j, rank: rank, want: w, got: g})
				break
			}
		}
	}()
}

// record notes how long the step showing took, the first pixels and the
// sharp ones.
func (cu *culler) record(what string, sharp bool) {
	for len(cu.timings) < cu.gen {
		cu.timings = append(cu.timings, timing{})
	}
	t := &cu.timings[cu.gen-1]
	d := time.Since(cu.stepped)
	if t.firstWhat == "" {
		t.first, t.firstWhat = d, what
	}
	if sharp && !t.sharpSeen {
		t.sharp, t.sharpSeen = d, true
	}
}

// script steps right o.skim times, o.every apart, then reports the timings
// and writes a picture of the window to o.shot, for measuring without
// hands.
// scriptStep takes a step of -keys that is not a key: click:x:y and
// move:x:y move the pointer, in the window, and click there, dclick:x:y
// twice; drag:x:y:x:y
// drags from one point to another; type:text
// types; shift+plus presses Shift and + as a Swedish keyboard does, which
// types ?; shift+ a key presses it with Shift; ctrl+z and w press those; wait:ms waits; and shot:name writes the window to name.png. It
// reports whether k was one.
func (cu *culler) scriptStep(k string) bool {
	verb, arg, ok := strings.Cut(k, ":")
	now := time.Now()
	switch {
	case ok && (verb == "click" || verb == "move" || verb == "dclick"):
		xs, ys, _ := strings.Cut(arg, ":")
		x, _ := strconv.ParseFloat(xs, 32)
		y, _ := strconv.ParseFloat(ys, 32)
		at := geom.Pt(float32(x), float32(y))
		_ = cu.c.Input(cu.ctx, input.PointerMove{Pos: at, Time: now})
		if verb == "click" || verb == "dclick" {
			time.Sleep(50 * time.Millisecond)
			clicks := map[bool]int{false: 1, true: 2}[verb == "dclick"]
			_ = cu.c.Input(cu.ctx, input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: clicks, Time: now})
			_ = cu.c.Input(cu.ctx, input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: now})
		}
	case ok && verb == "drag":
		var c [4]float64
		for i, f := range strings.SplitN(arg, ":", 4) {
			c[i], _ = strconv.ParseFloat(f, 64)
		}
		from, to := geom.Pt(float32(c[0]), float32(c[1])), geom.Pt(float32(c[2]), float32(c[3]))
		_ = cu.c.Input(cu.ctx, input.PointerMove{Pos: from, Time: now})
		_ = cu.c.Input(cu.ctx, input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Time: now})
		for i := 1; i <= 10; i++ {
			k := float32(i) / 10
			at := geom.Pt(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
			time.Sleep(30 * time.Millisecond)
			_ = cu.c.Input(cu.ctx, input.PointerMove{Pos: at, Time: time.Now()})
		}
		_ = cu.c.Input(cu.ctx, input.PointerUp{Pos: to, Button: input.ButtonPrimary, Time: time.Now()})
	case ok && verb == "scroll":
		// scroll:x:y:dy, a wheel turned at x, y by dy pixels, down negative.
		c := strings.Split(arg, ":")
		if len(c) == 3 {
			x, _ := strconv.ParseFloat(c[0], 32)
			y, _ := strconv.ParseFloat(c[1], 32)
			dy, _ := strconv.ParseFloat(c[2], 32)
			at := geom.Pt(float32(x), float32(y))
			_ = cu.c.Input(cu.ctx, input.PointerMove{Pos: at, Time: now})
			_ = cu.c.Input(cu.ctx, input.Scroll{Pos: at, Delta: geom.Pt(0, float32(dy)), Time: now})
		}
	case ok && verb == "type":
		_ = cu.c.Input(cu.ctx, input.TextInput{Text: arg, Time: now})
	case ok && verb == "fail":
		// A made-up failure, to see the error tray.
		cu.do <- func() { cu.fail(arg, errors.New("made up by the test script for "+arg)) }
	case k == "shift+plus":
		_ = cu.c.Input(cu.ctx, input.KeyPress{Key: input.KeyMinus, Mods: input.ModShift, Typed: true, Char: '+', Time: now})
		_ = cu.c.Input(cu.ctx, input.TextInput{Text: "?", Time: now})
	case ok && verb == "wait":
		ms, _ := strconv.Atoi(arg)
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return true
	case ok && verb == "shot":
		if err := writeShot(cu.ctx, cu.c, arg+".png"); err != nil {
			log.Print(err)
		}
		return true
	case strings.HasPrefix(k, "shift+") && namedKeys[strings.TrimPrefix(k, "shift+")] != 0:
		_ = cu.c.Input(cu.ctx, input.KeyPress{Key: namedKeys[strings.TrimPrefix(k, "shift+")], Mods: input.ModShift, Time: now})
	case k == "ctrl+z":
		_ = cu.c.Input(cu.ctx, input.KeyPress{Key: input.KeyZ, Mods: input.ModControl, Time: now})
	case k == "w":
		_ = cu.c.Input(cu.ctx, input.KeyPress{Key: input.KeyW, Time: now})
	default:
		return false
	}
	time.Sleep(300 * time.Millisecond)
	return true
}

func (cu *culler) script(o options) {
	n, every, shot, zoom, keys := o.skim, o.every, o.shot, o.zoom, o.keys
	time.Sleep(o.wait)
	if n > 0 || zoom {
		// Into the cull view on the first photo, as Enter on it does.
		cu.do <- func() { cu.openCull(0) }
		time.Sleep(time.Second)
	}
	for range n {
		cu.c.Input(cu.ctx, keyRight())
		time.Sleep(every)
	}
	time.Sleep(time.Second)
	pressed := strings.Split(keys, ",")
	for i, k := range pressed {
		if cu.scriptStep(strings.TrimSpace(k)) {
			continue
		}
		if key, ok := namedKeys[strings.TrimSpace(strings.ToLower(k))]; ok {
			cu.c.Input(cu.ctx, input.KeyPress{Key: key})
			// A burst starts with the last key, to catch what it animates.
			if i < len(pressed)-1 || o.burst == 0 {
				time.Sleep(300 * time.Millisecond)
			}
		}
	}
	for _, kv := range strings.Split(o.edit, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			continue
		}
		if key == "auto" {
			// An Auto button: wait for its result to show.
			cu.do <- func() { cu.devAuto(DevAuto{Sections: strings.Split(val, "+")}) }
			time.Sleep(4 * time.Second)
			fmt.Fprintf(os.Stderr, "edit: auto %s\n", val)
			continue
		}
		x, err := strconv.ParseFloat(val, 64)
		if err != nil {
			continue
		}
		start := time.Now()
		cu.do <- func() {
			if _, isChoice := devChoices[key]; isChoice {
				cu.devChoose(DevChoice{Key: key, Index: int(x)})
				return
			}
			cu.devSet(DevSet{Key: key, Value: x, Commit: true})
			// As the slider would show it, had it been dragged there.
			if cu.dev.mounted {
				_ = cu.c.Update("develop", cu.developState())
			}
		}
		// Until the full-size preview of it shows, or a while.
		for time.Since(start) < 20*time.Second {
			time.Sleep(100 * time.Millisecond)
			done := make(chan bool, 1)
			cu.do <- func() { done <- !cu.dev.busy && !cu.dev.want && cu.dev.live != nil }
			if <-done {
				break
			}
		}
		note := make(chan string, 1)
		cu.do <- func() { note <- cu.dev.note }
		fmt.Fprintf(os.Stderr, "edit: %s=%v shown after %d ms (%s)\n", key, x, time.Since(start).Milliseconds(), <-note)
	}
	if zoom {
		start := time.Now()
		cu.c.Input(cu.ctx, keyZ())
		// Until the tiles in view are in, or a while.
		for time.Since(start) < 20*time.Second {
			time.Sleep(200 * time.Millisecond)
			done := make(chan bool, 1)
			cu.do <- func() {
				r := cu.tileWant.Range
				done <- !r.Empty() && len(cu.tilesShowing(cu.photos[cu.at])) >= r.Dx()*r.Dy()
			}
			if <-done {
				break
			}
		}
		took := time.Since(start)
		done := make(chan string, 1)
		cu.do <- func() { done <- cu.tileNote }
		fmt.Fprintf(os.Stderr, "zoom: tiles in view after %d ms (%s)\n", took.Milliseconds(), <-done)
		time.Sleep(500 * time.Millisecond)
	}
	if shot != "" && o.burst > 0 {
		// A run of pictures, name-01.png on, to see the motion.
		base := strings.TrimSuffix(shot, ".png")
		for i := range o.burst {
			if err := writeShot(cu.ctx, cu.c, fmt.Sprintf("%s-%02d.png", base, i+1)); err != nil {
				log.Print(err)
			}
			time.Sleep(30 * time.Millisecond)
		}
	} else if shot != "" {
		if err := writeShot(cu.ctx, cu.c, shot); err != nil {
			log.Print(err)
		}
	}
	if n > 0 || zoom || keys != "" || shot != "" || o.edit != "" {
		cu.do <- func() {
			cu.report()
			cu.c.Leave()
		}
	}
}

// report prints the steps' timings.
func (cu *culler) report() {
	var firsts, sharps []time.Duration
	var lines []string
	for i, t := range cu.timings {
		if t.firstWhat == "" {
			lines = append(lines, fmt.Sprintf("step %2d: nothing shown before the next step", i))
			continue
		}
		firsts = append(firsts, t.first)
		if t.sharpSeen {
			sharps = append(sharps, t.sharp)
		}
		lines = append(lines, fmt.Sprintf("step %2d: first %4d ms (%s), sharp %4d ms", i, t.first.Milliseconds(), t.firstWhat, t.sharp.Milliseconds()))
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
	fmt.Fprintf(os.Stderr, "steps %d; first pixels median %d ms, max %d ms; sharp in %d of them, median %d ms\n",
		len(cu.timings), median(firsts).Milliseconds(), slices.Max(append(firsts, 0)).Milliseconds(), len(sharps), median(sharps).Milliseconds())
}

func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}

// pixelCache keeps the decoded pixels of the photos seen and warmed last.
type pixelCache struct {
	mu    sync.Mutex
	limit int
	order []int64
	m     map[int64]cacheEntry
}

type cacheEntry struct {
	img  *paint.Image
	rank int
	note string
}

func newPixelCache(limit int) *pixelCache {
	return &pixelCache{limit: limit, m: map[int64]cacheEntry{}}
}

// drop forgets id's pixels, as after an edit.
func (pc *pixelCache) drop(id int64) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	delete(pc.m, id)
	pc.order = slices.DeleteFunc(pc.order, func(x int64) bool { return x == id })
}

func (pc *pixelCache) get(id int64) (cacheEntry, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	e, ok := pc.m[id]
	return e, ok
}

// put keeps e for id unless a better one is kept, and lets the oldest go.
func (pc *pixelCache) put(id int64, e cacheEntry) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if old, ok := pc.m[id]; ok && old.rank > e.rank {
		return
	}
	pc.m[id] = e
	pc.order = slices.DeleteFunc(pc.order, func(x int64) bool { return x == id })
	pc.order = append(pc.order, id)
	for len(pc.order) > pc.limit {
		delete(pc.m, pc.order[0])
		pc.order = pc.order[1:]
	}
}

// loadStrip fetches the filmstrip's pictures not fetched yet, nearest
// first.
func (cu *culler) loadStrip() {
	for d := 0; d <= stripReach; d++ {
		for _, i := range []int{cu.at + d, cu.at - d} {
			if i >= 0 && i < len(cu.photos) {
				cu.loadThumb(cu.ctx, i)
			}
		}
	}
}

// thumbKeep is how many small pictures are kept.
const thumbKeep = 800

// loadThumb fetches photo i's small picture unless it is in or on its way:
// the 256 at any edit state, never decoding a RAW, a few at a time. It
// shows in the grid and the filmstrip as it comes.
func (cu *culler) loadThumb(ctx context.Context, i int) {
	p := cu.photos[i]
	if cu.thumbs[p.ID] != nil || cu.thumbsWanted[p.ID] {
		return
	}
	cu.thumbsWanted[p.ID] = true
	go func() {
		var g got
		err := ctx.Err()
		if err == nil {
			select {
			case cu.thumbSlots <- struct{}{}:
				g, err = cu.im.get(ctx, p, want{level: "256", stale: true, fast: true})
				<-cu.thumbSlots
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		select {
		case cu.do <- func() {
			delete(cu.thumbsWanted, p.ID)
			if err != nil {
				return
			}
			cu.keepThumb(p.ID, g.img)
			cu.learnShape(p.ID, g.w, g.h)
			// Where the photo is now: the folder may have changed.
			i, ok := cu.index[p.ID]
			if !ok {
				return
			}
			_ = cu.c.Patch("grid", ThumbIn{Folder: cu.folder, Index: i, Img: g.img})
			if cu.culling && i >= cu.at-stripReach && i <= cu.at+stripReach {
				cu.showCull()
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// refreshThumb brings photo p's small picture of its new edit, the old
// one showing until it comes: the backend renders it as the edit is
// saved, so it is asked for from what is rendered, every little while,
// for a while. Asked for at any edit state, the old one would come back.
func (cu *culler) refreshThumb(p marrawclient.Photo) {
	go func() {
		for range 40 {
			g, err := cu.im.get(cu.ctx, p, want{level: "256", cacheOnly: true})
			if err == nil {
				select {
				case cu.do <- func() {
					// Only while the photo is still at that edit.
					i, ok := cu.index[p.ID]
					if !ok || cu.photos[i].EditHash != p.EditHash {
						return
					}
					cu.keepThumb(p.ID, g.img)
					cu.learnShape(p.ID, g.w, g.h)
					_ = cu.c.Patch("grid", ThumbIn{Folder: cu.folder, Index: i, Img: g.img})
					if cu.culling && i >= cu.at-stripReach && i <= cu.at+stripReach {
						cu.showCull()
					}
				}:
				case <-cu.ctx.Done():
				}
				return
			}
			select {
			case <-time.After(400 * time.Millisecond):
			case <-cu.ctx.Done():
				return
			}
		}
	}()
}

// keepThumb keeps id's small picture, letting the oldest go, in the grid
// too.
func (cu *culler) keepThumb(id int64, img *paint.Image) {
	if _, ok := cu.thumbs[id]; !ok {
		cu.thumbOrder = append(cu.thumbOrder, id)
	}
	cu.thumbs[id] = img
	for len(cu.thumbOrder) > thumbKeep {
		gone := cu.thumbOrder[0]
		delete(cu.thumbs, gone)
		cu.thumbOrder = cu.thumbOrder[1:]
		if i, ok := cu.index[gone]; ok {
			_ = cu.c.Patch("grid", ThumbIn{Folder: cu.folder, Index: i})
		}
	}
}

// rate gives stars: to the photo showing, or to the grid's selection,
// here at once and on the backend.
func (cu *culler) rate(in Rate) {
	stars := max(0, min(in.Stars, 5))
	idx := cu.targetsOf(in.ID)
	if in.Toggle && len(idx) > 0 {
		all := true
		for _, i := range idx {
			all = all && cu.photos[i].Rating == stars
		}
		if all {
			stars = 0
		}
	}
	var ids []int64
	step := cullStep{what: map[bool]string{false: fmt.Sprintf("%d stars", stars), true: "rating cleared"}[stars == 0]}
	for _, i := range idx {
		p := &cu.photos[i]
		step.ids = append(step.ids, p.ID)
		step.before = append(step.before, photoMark{p.Rating, p.Flag})
		p.Rating = stars
		step.after = append(step.after, photoMark{p.Rating, p.Flag})
		ids = append(ids, p.ID)
		cu.marked(i)
	}
	if len(ids) == 0 {
		return
	}
	cu.recordCull(step)
	go func() {
		if err := cu.api.Library.SetRating(cu.ctx, ids, stars); err != nil {
			log.Printf("rate: %v", err)
		}
	}()
	cu.refilter()
}

// mark flags the photo showing, or the grid's selection, here at once and
// on the backend.
func (cu *culler) mark(f marrawclient.Flag, id int64) {
	idx := cu.targetsOf(id)
	// P on a photo picked already, or X on one rejected, takes the flag
	// off again, as marraw's keys do.
	if f != "none" && len(idx) > 0 {
		all := true
		for _, i := range idx {
			all = all && cu.photos[i].Flag == f
		}
		if all {
			f = "none"
		}
	}
	var ids []int64
	step := cullStep{what: map[marrawclient.Flag]string{"pick": "pick", "exclude": "reject", "none": "flag cleared"}[f]}
	for _, i := range idx {
		p := &cu.photos[i]
		step.ids = append(step.ids, p.ID)
		step.before = append(step.before, photoMark{p.Rating, p.Flag})
		p.Flag = f
		step.after = append(step.after, photoMark{p.Rating, p.Flag})
		ids = append(ids, p.ID)
		cu.marked(i)
	}
	if len(ids) == 0 {
		return
	}
	cu.recordCull(step)
	go func() {
		if err := cu.api.Library.SetFlag(cu.ctx, ids, f); err != nil {
			log.Printf("flag: %v", err)
		}
	}()
	cu.refilter()
}

// targets are the photos a rating or a flag goes to: the one showing, or
// the grid's selection, or the photo its keyboard is on.
// targetsOf is photo id alone, where it shows, or without one the
// targets.
func (cu *culler) targetsOf(id int64) []int {
	if id == 0 {
		return cu.targets()
	}
	if i, ok := cu.index[id]; ok {
		return []int{i}
	}
	return nil
}

func (cu *culler) targets() []int {
	if cu.culling {
		return []int{cu.at}
	}
	var out []int
	for _, r := range cu.sel {
		for i := r[0]; i < r[1] && i < len(cu.photos); i++ {
			out = append(out, i)
		}
	}
	if len(out) == 0 && cu.cursor >= 0 && cu.cursor < len(cu.photos) {
		out = append(out, cu.cursor)
	}
	return out
}

// marked shows photo i's rating and flag wherever it shows.
func (cu *culler) marked(i int) {
	p := cu.photos[i]
	_ = cu.c.Patch("grid", PhotoMarked{Folder: cu.folder, Index: i, Rating: p.Rating, Flag: string(p.Flag)})
	if cu.culling && i >= cu.at-stripReach && i <= cu.at+stripReach {
		cu.showCull()
	}
}

// patched takes changes to photos made anywhere: their ratings and flags,
// and an edit, whose new pixels are fetched again.
func (cu *culler) patched(ps []marrawclient.PhotoPatch) {
	// The folder's whole list takes them too, for the photos not showing.
	aided := false
	for _, pp := range ps {
		aided = aided || pp.SubjectSharpness != nil || pp.EyesClosed != nil || pp.EyesAnalyzed != nil || pp.SubjectAnalyzed != nil
		ai, ok := cu.allIndex[pp.ID]
		if !ok {
			continue
		}
		takeAids(&cu.all[ai], pp)
		takeShape(&cu.all[ai], pp)
		if i, showing := cu.index[pp.ID]; showing {
			takeAids(&cu.photos[i], pp)
			takeShape(&cu.photos[i], pp)
			continue
		}
		p := &cu.all[ai]
		if pp.Rating != nil {
			p.Rating = *pp.Rating
		}
		if pp.Flag != nil {
			p.Flag = *pp.Flag
		}
		if pp.EditHash != nil {
			p.EditHash = *pp.EditHash
		}
	}
	if aided {
		defer cu.aidsChanged()
	}
	changed := false
	for _, pp := range ps {
		for i := range cu.photos {
			p := &cu.photos[i]
			if p.ID != pp.ID {
				continue
			}
			if pp.Rating != nil {
				p.Rating = *pp.Rating
			}
			if pp.Flag != nil {
				p.Flag = *pp.Flag
			}
			if pp.Rating != nil || pp.Flag != nil {
				_ = cu.c.Patch("grid", PhotoMarked{Folder: cu.folder, Index: i, Rating: p.Rating, Flag: string(p.Flag)})
			}
			if pp.EditHash != nil && cu.dev.saved[p.ID] {
				// The panel's own save, whose pixels show already: the
				// tiles of it can come now.
				delete(cu.dev.saved, p.ID)
				changed := *pp.EditHash != p.EditHash
				p.EditHash = *pp.EditHash
				cu.dev.confirmed = cu.dev.committed
				if cu.tileNote == "tiles: once the edit is saved" {
					cu.tileNote = ""
				}
				if cu.culling && i == cu.at && !cu.tileWant.Range.Empty() {
					cu.wantTiles(cu.tileWant)
				}
				if changed {
					cu.refreshThumb(*p)
				}
			}
			if pp.EditHash != nil && *pp.EditHash != p.EditHash {
				p.EditHash = *pp.EditHash
				cu.tiles.drop(p.ID)
				if cu.dev.open && cu.dev.id == p.ID {
					// Edited elsewhere: the panel takes the new edit.
					cu.loadEdit(i)
				}
				cu.cache.drop(p.ID)
				cu.refreshThumb(*p)
				if cu.culling && i == cu.at {
					// Again, with the new edit's pixels.
					cu.load = nil
					cu.goTo(i)
				}
			}
			if i >= cu.at-stripReach && i <= cu.at+stripReach {
				changed = true
			}
		}
	}
	if changed {
		cu.showCull()
		cu.loadStrip()
	}
	cu.refilter()
}
