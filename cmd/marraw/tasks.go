package main

import (
	"slices"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// TaskNote is a background task as the tray shows it: what it is, how far
// it has got of how much, in Unit, and whether it is done.
type TaskNote struct {
	ID, Title, Unit string
	Current, Total  int
	Done            bool
}

// TasksIn are the tasks to show, and TaskCancel cancels one.
type (
	TasksIn    struct{ List []TaskNote }
	TaskCancel struct{ ID string }
)

// taskShowAfter is how long a task runs before the tray shows it, so a
// quick one, such as an AI mask whose model is there already, does not
// flash a card; taskDoneFor is how long a finished one stays, ticked.
const (
	taskShowAfter = 400 * time.Millisecond
	taskDoneFor   = 1500 * time.Millisecond
)

// taskRun is what the culler keeps of a task: its note, and when it was
// first seen.
type taskRun struct {
	note TaskNote
	seen time.Time
	// sub is the subtask the chip shows, as a model downloading inside a
	// scan, or "".
	sub string
}

// trackTasks takes the backend's tasks as they stand: new ones are noted,
// finished ones tick and go a moment later, failed ones go at once, their
// failure told where the task is the user's own.
func (cu *culler) trackTasks(ts []marrawclient.SharedTaskState) {
	if cu.tasks == nil {
		cu.tasks, cu.taskOf = map[string]*taskRun{}, map[string]string{}
	}
	now := time.Now()
	live := map[string]bool{}
	for _, t := range ts {
		if t.ParentID != "" {
			continue
		}
		live[t.ID] = true
		run, ok := cu.tasks[t.ID]
		if !ok {
			run = &taskRun{seen: now}
			cu.tasks[t.ID] = run
			cu.wakeTasksAt(now.Add(taskShowAfter))
		}
		unit := ""
		if t.Meta != nil {
			unit = t.Meta.Unit
		}
		run.note = TaskNote{ID: t.ID, Title: t.Title, Unit: unit, Current: t.Current, Total: t.Total, Done: run.note.Done}
		// A subtask under way, as a model downloading inside a scan, shows
		// in its place: its progress is the one moving.
		run.sub = ""
		for _, c := range t.Children {
			if c.Status != marrawclient.TaskNodeStatusRunning && c.Status != marrawclient.TaskNodeStatusCreated {
				continue
			}
			run.sub = c.ID
			cu.taskOf[c.ID] = t.ID
			run.note.Title, run.note.Current, run.note.Total, run.note.Unit = c.Title, c.Current, c.Total, ""
			if c.Meta != nil {
				run.note.Unit = c.Meta.Unit
			}
		}
		switch t.Status {
		case marrawclient.TaskNodeStatusCompleted:
			if time.Since(run.seen) < taskShowAfter {
				// Done before it ever showed: nothing to tell.
				delete(cu.tasks, t.ID)
				continue
			}
			if !run.note.Done {
				run.note.Done = true
				id := t.ID
				time.AfterFunc(taskDoneFor, func() {
					select {
					case cu.do <- func() { delete(cu.tasks, id); cu.showTasks() }:
					case <-cu.ctx.Done():
					}
				})
			}
		case marrawclient.TaskNodeStatusFailed:
			delete(cu.tasks, t.ID)
		}
	}
	// A task the backend no longer lists has ended.
	for id, run := range cu.tasks {
		if !live[id] && !run.note.Done {
			delete(cu.tasks, id)
		}
	}
	for sub, parent := range cu.taskOf {
		if run, ok := cu.tasks[parent]; !ok || run.sub != sub {
			delete(cu.taskOf, sub)
		}
	}
	cu.showTasks()
}

// taskMoved takes how far a task has got.
func (cu *culler) taskMoved(id string, current, total int) {
	run, ok := cu.tasks[id]
	if !ok {
		// A subtask's: it moves its task's chip while the chip shows it.
		if parent, sub := cu.taskOf[id]; sub {
			if run, ok = cu.tasks[parent]; !ok || run.sub != id {
				return
			}
		}
	} else if run.sub != "" {
		// The chip shows the subtask's progress, not its own.
		return
	}
	if run == nil {
		return
	}
	run.note.Current, run.note.Total = current, total
	cu.showTasks()
}

// wakeTasksAt shows the tasks again at t, for one to come into the tray.
func (cu *culler) wakeTasksAt(t time.Time) {
	time.AfterFunc(time.Until(t), func() {
		select {
		case cu.do <- cu.showTasks:
		case <-cu.ctx.Done():
		}
	})
}

// shownTasks are the tasks the tray shows, oldest first: those that have
// run a moment.
func (cu *culler) shownTasks() []TaskNote {
	runs := make([]*taskRun, 0, len(cu.tasks))
	for _, run := range cu.tasks {
		if time.Since(run.seen) >= taskShowAfter {
			runs = append(runs, run)
		}
	}
	slices.SortFunc(runs, func(a, b *taskRun) int { return a.seen.Compare(b.seen) })
	out := make([]TaskNote, len(runs))
	for i, run := range runs {
		out[i] = run.note
	}
	return out
}

// showTasks shows the tasks over the grid, and over the photo when
// the cull view is open.
func (cu *culler) showTasks() {
	_ = cu.c.Patch("grid", TasksIn{List: cu.shownTasks()})
	if cu.culling {
		cu.showCull()
	}
}

// cancelTask asks the backend to cancel the task with id.
func (cu *culler) cancelTask(id string) {
	go func() { _ = cu.api.TasksHandler.CancelTask(cu.ctx, id) }()
}
