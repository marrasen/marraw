package main

import (
	"context"
	"errors"
	"log"
)

// ErrorNote is an error the user has not cleared yet: what failed, in
// words, the error itself, and how many times it came.
type ErrorNote struct {
	ID           int
	Text, Detail string
	Count        int
}

// Errors are the errors to show, and ErrClear clears the one with ID, or
// all of them.
type (
	ErrorsIn struct{ List []ErrorNote }
	ErrClear struct {
		ID  int
		All bool
	}
)

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
