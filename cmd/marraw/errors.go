package main

import (
	"context"
	"errors"
	"log"
	"strings"
)

// ErrorNote is an error the user has not cleared yet: what failed, in
// words, the error itself, and how many times it came.
type ErrorNote struct {
	ID           int
	Text, Detail string
	Count        int
	// Done makes the note news of work finished, not a failure: a tick
	// in place of the warning. Action names a button on it, as "Open
	// folder", and Path what the button opens.
	Done         bool
	Action, Path string
}

// Errors are the errors to show, and ErrClear clears the one with ID, or
// all of them.
type (
	ErrorsIn struct{ List []ErrorNote }
	ErrClear struct {
		ID  int
		All bool
	}
	// ErrAction does what the note ID's button says.
	ErrAction struct{ ID int }
)

// done tells the user work has finished, in a note that stays until they
// clear it or take its action, the button action opening path.
func (cu *culler) done(what, detail, action, path string) {
	cu.errSeq++
	cu.errs = append(cu.errs, ErrorNote{ID: cu.errSeq, Text: what, Detail: detail, Count: 1, Done: true, Action: action, Path: path})
	if len(cu.errs) > maxErrors {
		cu.errs = cu.errs[len(cu.errs)-maxErrors:]
	}
	cu.errorsChanged()
}

// errAction opens what the note's button names, and clears the note.
func (cu *culler) errAction(id int) {
	for _, e := range cu.errs {
		if e.ID == id && strings.HasPrefix(e.Path, updateMark) {
			cu.clearError(ErrClear{ID: id})
			cu.showUpdate()
			return
		}
		if e.ID == id && strings.HasPrefix(e.Path, whatsNewMark) {
			cu.clearError(ErrClear{ID: id})
			cu.showWhatsNew(strings.TrimPrefix(e.Path, whatsNewMark))
			return
		}
		if e.ID == id && e.Path == restartMark {
			cu.clearError(ErrClear{ID: id})
			cu.restartIntoUpdate()
			return
		}
		if e.ID == id && e.Path != "" {
			path := e.Path
			go func() {
				if err := cu.c.Open(path); err != nil {
					cu.onDo(func() { cu.fail("The folder could not be opened", err) })
				}
			}()
		}
	}
	cu.clearError(ErrClear{ID: id})
}

// orNoAnswer is err, or an error saying the backend gave no answer where
// it gave neither an answer nor an error.
func orNoAnswer(err error) error {
	if err == nil {
		return errors.New("the backend gave no answer")
	}
	return err
}

// maxErrors is how many errors stay at once: older ones go.
const maxErrors = 5

// canceled reports whether err is work called off, which is no error to
// the user: moving to the next photo cancels the last one's fetches.
func canceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

// fail tells the user what failed, in a note that stays until cleared,
// with err under it. A canceled err is not a failure, and says nothing.
// The same failure again counts up rather than adding another note.
func (cu *culler) fail(what string, err error) {
	if err == nil || canceled(err) {
		return
	}
	log.Printf("%s: %v", what, err)
	detail := err.Error()
	for i, e := range cu.errs {
		if e.Text == what && e.Detail == detail {
			e.Count++
			cu.errs = append(append(cu.errs[:i:i], cu.errs[i+1:]...), e)
			cu.errorsChanged()
			return
		}
	}
	cu.errSeq++
	cu.errs = append(cu.errs, ErrorNote{ID: cu.errSeq, Text: what, Detail: detail, Count: 1})
	if len(cu.errs) > maxErrors {
		cu.errs = cu.errs[len(cu.errs)-maxErrors:]
	}
	cu.errorsChanged()
}

// clearError clears what in asks.
func (cu *culler) clearError(in ErrClear) {
	if in.All {
		cu.errs = nil
	} else {
		for i, e := range cu.errs {
			if e.ID == in.ID {
				cu.errs = append(cu.errs[:i:i], cu.errs[i+1:]...)
				break
			}
		}
	}
	cu.errorsChanged()
}

// errorsChanged shows the errors over the grid, and over the photo when
// the cull view is open.
func (cu *culler) errorsChanged() {
	list := append([]ErrorNote(nil), cu.errs...)
	_ = cu.c.Patch("grid", ErrorsIn{List: list})
	if cu.culling {
		cu.showCull()
	}
}
