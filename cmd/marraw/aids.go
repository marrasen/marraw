package main

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/marrasen/gunim"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// Aids are what marraw tells of a photo to help cull it: its place in a
// burst, and whether it is soft or someone's eyes are closed.
type Aids struct {
	// Burst is the photo's place in its burst, from one, of BurstOf
	// frames, and BurstBest says it is the burst's sharpest; nought out
	// of a burst.
	Burst, BurstOf int
	BurstBest      bool
	// Soft says the photo is soft for the shoot, and Eyes that someone's
	// eyes are likely closed.
	Soft, Eyes bool
}

// eyesClosedAt is how sure the eye model must be that eyes are closed
// for the photo to say so, as marraw's.
const eyesClosedAt = 0.5

// burst is a burst's frames, by id in the order showing, and its
// sharpest.
type burst struct {
	ids  []int64
	best int64
}

// aidsOf works out the aids of the folder's photos: the shoot's softness
// from all of them, and its bursts in order.
type aidsOf struct {
	softBelow float64
	bursts    map[int64]*burst
	// off are the culling aids turned off in the settings, by their ids.
	off map[string]bool
}

// focusScore is how sharp photo p is, its subject's sharpness where known.
func focusScore(p marrawclient.Photo) (float64, bool) {
	if p.SubjectSharpness != nil {
		return *p.SubjectSharpness, true
	}
	if p.Sharpness != nil {
		return *p.Sharpness, true
	}
	return 0, false
}

// newAids reads the folder's photos, all, in the view's order.
func newAids(all []marrawclient.Photo, v LibView) aidsOf {
	a := aidsOf{bursts: map[int64]*burst{}}
	// Soft is soft for this shoot: well under its middling sharpness,
	// once there are enough scores to say, as marraw judges it.
	var scores []float64
	for _, p := range all {
		if p.Sharpness != nil {
			scores = append(scores, *p.Sharpness)
		}
	}
	if len(scores) >= 4 {
		sort.Float64s(scores)
		a.softBelow = max(50, scores[len(scores)/2]/15)
	}
	ordered := append([]marrawclient.Photo(nil), all...)
	sort.SliceStable(ordered, func(i, j int) bool { return v.less(ordered[i], ordered[j]) })
	members := map[int64][]marrawclient.Photo{}
	for _, p := range ordered {
		if p.GroupID != nil {
			members[*p.GroupID] = append(members[*p.GroupID], p)
		}
	}
	for g, ps := range members {
		b := &burst{}
		// The sharpest by the subject where every frame has a subject's
		// score, and by the whole frame otherwise.
		bySubject := true
		for _, p := range ps {
			b.ids = append(b.ids, p.ID)
			bySubject = bySubject && p.SubjectSharpness != nil
		}
		best := -1.0
		for _, p := range ps {
			var s *float64
			if bySubject {
				s = p.SubjectSharpness
			} else {
				s = p.Sharpness
			}
			if s != nil && *s > best {
				best, b.best = *s, p.ID
			}
		}
		a.bursts[g] = b
	}
	return a
}

// of is photo p's aids.
func (a aidsOf) of(p marrawclient.Photo) Aids {
	out := a.on(p)
	if a.off["softFilter"] {
		out.Soft = false
	}
	if a.off["eyes"] {
		out.Eyes = false
	}
	if a.off["bursts"] {
		out.Burst, out.BurstOf, out.BurstBest = 0, 0, false
	}
	return out
}

// on is photo p's aids, all of them on.
func (a aidsOf) on(p marrawclient.Photo) Aids {
	var out Aids
	if s, ok := focusScore(p); ok && a.softBelow > 0 {
		out.Soft = s < a.softBelow
	}
	out.Eyes = p.EyesClosed != nil && *p.EyesClosed >= eyesClosedAt
	if p.GroupID != nil {
		if b := a.bursts[*p.GroupID]; b != nil {
			for i, id := range b.ids {
				if id == p.ID {
					out.Burst = i + 1
				}
			}
			out.BurstOf, out.BurstBest = len(b.ids), b.best == p.ID
		}
	}
	return out
}

// lead is the frame a collapsed burst shows: its sharpest, or its first
// until frames have scores.
func (b *burst) lead() int64 {
	if b.best != 0 {
		return b.best
	}
	return b.ids[0]
}

// aidsNote is what the photo view's readout says of aids a.
func aidsNote(a Aids) string {
	var parts []string
	if a.BurstOf > 0 {
		s := fmt.Sprintf("burst %d/%d", a.Burst, a.BurstOf)
		if a.BurstBest {
			s += ", sharpest"
		}
		parts = append(parts, s)
	}
	if a.Soft {
		parts = append(parts, "soft")
	}
	if a.Eyes {
		parts = append(parts, "eyes closed?")
	}
	return strings.Join(parts, " · ")
}

// The aids' vocabulary.
type (
	// BurstKeep keeps the photo the keys are on of its burst: the rest
	// of the burst are rejected, and with Pick it is picked, as Shift+P
	// and Shift+X do in marraw.
	BurstKeep struct{ Pick bool }
	// JudgeBursts picks each burst's sharpest frame and rejects the rest.
	JudgeBursts struct{}
	// CheckEyes has the backend look for closed eyes in the photos not
	// looked at yet, and CheckSubjects for the subjects, to judge their
	// sharpness; Download allows the model to be fetched.
	CheckEyes     struct{ Download bool }
	CheckSubjects struct{ Download bool }
)

// remarkAll gives the photos the marks in to, as one step to undo named
// what, here and on the backend.
func (cu *culler) remarkAll(what string, to map[int64]photoMark) {
	if len(to) == 0 {
		return
	}
	step := cullStep{what: what}
	ratings := map[int][]int64{}
	flags := map[marrawclient.Flag][]int64{}
	for id, m := range to {
		was, ok := cu.markOf(id)
		if !ok || was == m {
			continue
		}
		step.ids = append(step.ids, id)
		step.before = append(step.before, was)
		step.after = append(step.after, m)
		cu.remark(id, m)
		if m.rating != was.rating {
			ratings[m.rating] = append(ratings[m.rating], id)
		}
		if m.flag != was.flag {
			flags[m.flag] = append(flags[m.flag], id)
		}
	}
	if len(step.ids) == 0 {
		return
	}
	cu.recordCull(step)
	go func() {
		for r, ids := range ratings {
			if err := cu.api.Library.SetRating(cu.ctx, ids, r); err != nil {
				log.Printf("%s: %v", what, err)
			}
		}
		for f, ids := range flags {
			if err := cu.api.Library.SetFlag(cu.ctx, ids, f); err != nil {
				log.Printf("%s: %v", what, err)
			}
		}
	}()
	cu.refilter()
}

// burstKeep keeps the photo the keys are on of its burst.
func (cu *culler) burstKeep(pick bool) {
	idx := cu.targets()
	if len(idx) != 1 {
		return
	}
	p := cu.photos[idx[0]]
	b := cu.aids.bursts[groupOf(p)]
	if b == nil {
		cu.notify("Not in a burst")
		return
	}
	to := map[int64]photoMark{}
	for _, id := range b.ids {
		m, _ := cu.markOf(id)
		if id == p.ID {
			if pick {
				m.flag = "pick"
			}
		} else {
			m.flag = "exclude"
		}
		to[id] = m
	}
	what := "kept the frame, rejected the rest of its burst"
	if pick {
		what = "picked the frame, rejected the rest of its burst"
	}
	cu.remarkAll(what, to)
	cu.notify(strings.ToUpper(what[:1]) + what[1:])
}

// groupOf is p's burst, or nought.
func groupOf(p marrawclient.Photo) int64 {
	if p.GroupID == nil {
		return 0
	}
	return *p.GroupID
}

// judgeBursts picks each burst's sharpest frame and rejects the rest, as
// marraw's Auto-judge does: a burst with no scores yet, or with another
// frame picked already, is left alone.
func (cu *culler) judgeBursts() {
	to := map[int64]photoMark{}
	judged := 0
	for _, b := range cu.aids.bursts {
		if b.best == 0 {
			continue
		}
		other := false
		for _, id := range b.ids {
			if m, _ := cu.markOf(id); id != b.best && m.flag == "pick" {
				other = true
			}
		}
		if other {
			continue
		}
		judged++
		for _, id := range b.ids {
			m, _ := cu.markOf(id)
			m.flag = map[bool]marrawclient.Flag{false: "exclude", true: "pick"}[id == b.best]
			to[id] = m
		}
	}
	if judged == 0 {
		cu.notify("No bursts to judge: they need sharpness scores, and none of another frame picked")
		return
	}
	cu.remarkAll(fmt.Sprintf("judged %d bursts", judged), to)
	cu.notify(fmt.Sprintf("Judged %d %s: the sharpest picked, the rest rejected", judged, map[bool]string{false: "bursts", true: "burst"}[judged == 1]))
}

// takeAids copies the aids patch pp carries into p.
func takeAids(p *marrawclient.Photo, pp marrawclient.PhotoPatch) {
	if pp.SubjectSharpness != nil {
		p.SubjectSharpness = pp.SubjectSharpness
	}
	if pp.SubjectAnalyzed != nil {
		p.SubjectAnalyzed = *pp.SubjectAnalyzed
	}
	if pp.EyesClosed != nil {
		p.EyesClosed = pp.EyesClosed
	}
	if pp.EyesAnalyzed != nil {
		p.EyesAnalyzed = *pp.EyesAnalyzed
	}
}

// aidsChanged shows the aids anew, as the backend's analysis comes in:
// the filters take them, and the badges show them.
func (cu *culler) aidsChanged() {
	if cu.folder == 0 {
		return
	}
	cu.syncAll()
	cu.aids = newAids(cu.all, cu.libView)
	cu.aids.off = cu.featuresOff()
	cu.refilter()
	_ = cu.c.Update("grid", cu.gridState())
	cu.showCull()
}

// scanRun is an analysis under way, for the notes of how it goes: what
// it does, and to how many photos.
type scanRun struct {
	what  string
	count int
}

// checkEyes has the backend look for closed eyes in the folder's photos
// not looked at yet, asking first to fetch the model where it is missing.
func (cu *culler) checkEyes(download bool) {
	var ids []int64
	for _, p := range cu.all {
		if !p.EyesAnalyzed {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		cu.notify("Every photo here has had its eyes checked")
		return
	}
	go func() {
		if !download {
			if m, err := cu.api.Library.EyeModelStatus(cu.ctx); err == nil && m != nil && !m.Downloaded {
				cu.askModel("eyesModel", "the eye model", m.Bytes)
				return
			}
		}
		ref, err := cu.api.Library.AnalyzeEyes(cu.ctx, ids, download)
		cu.scanStarted("Checking eyes", len(ids), ref, err)
	}()
}

// checkSubjects has the backend find the subjects of the folder's photos
// not looked at yet, to judge how sharp each is where it matters.
func (cu *culler) checkSubjects(download bool) {
	var ids []int64
	for _, p := range cu.all {
		if !p.SubjectAnalyzed {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		cu.notify("Every photo here has had its subject found")
		return
	}
	go func() {
		if !download {
			if m, err := cu.api.Edits.AIModelStatus(cu.ctx, marrawclient.AIKindSubject); err == nil && m != nil && !m.Downloaded {
				cu.askModel("subjectModel", "the subject model", m.Bytes)
				return
			}
		}
		ref, err := cu.api.Edits.AnalyzeSubjects(cu.ctx, ids, download)
		cu.scanStarted("Finding subjects", len(ids), ref, err)
	}()
}

// askModel asks, on the culler's goroutine, to fetch a model of so many
// bytes, as kind's answer.
func (cu *culler) askModel(kind, what string, bytes int64) {
	select {
	case cu.do <- func() {
		if cu.asking {
			return
		}
		cu.asking = true
		body := "marraw needs " + what + " for this, and does not have it yet."
		if bytes > 0 {
			mb := float64(bytes) / 1e6
			size := fmt.Sprintf("%.0f MB", mb)
			if mb < 10 {
				size = fmt.Sprintf("%.1f MB", mb)
			}
			body = fmt.Sprintf("marraw needs %s for this, %s, and does not have it yet.", what, size)
		}
		_ = cu.c.Mount(gunim.Root, "confirm", "confirm", ConfirmAsk{Kind: kind, Title: "Download " + what + "?",
			Body: body + " It is fetched once, and runs on this computer.", OK: "Download"})
	}:
	case <-cu.ctx.Done():
	}
}

// scanStarted takes, on the culler's goroutine, the start of an analysis
// of n photos, saying what is under way.
func (cu *culler) scanStarted(what string, n int, ref *marrawclient.TaskRef, err error) {
	select {
	case cu.do <- func() {
		if err != nil {
			cu.fail(what+" could not start", err)
			return
		}
		if ref == nil {
			return
		}
		cu.scans[ref.TaskID] = scanRun{what: what, count: n}
		cu.notify(fmt.Sprintf("%s in %d %s…", what, n, map[bool]string{false: "photos", true: "photo"}[n == 1]))
	}:
	case <-cu.ctx.Done():
	}
}

// scanState takes an analysis's state as it ends, the tray showing how
// it goes; it reports whether t was one.
func (cu *culler) scanState(t marrawclient.SharedTaskState) bool {
	run, ok := cu.scans[t.ID]
	if !ok {
		return false
	}
	switch t.Status {
	case "completed":
		delete(cu.scans, t.ID)
		cu.notify(run.what + ": done")
	case "failed":
		delete(cu.scans, t.ID)
		why := t.Error
		if why == "" {
			why = "no reason given"
		}
		cu.fail(run.what+" failed", errors.New(why))
	}
	return true
}
