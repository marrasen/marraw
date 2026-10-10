package main

import (
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

type (
	// PromptAsk asks for a line of text: Kind says what it is for, and
	// Key what it names.
	PromptAsk struct {
		Kind, Key, Title, Label, Value, OK string
	}
	// PromptDone is the text given, or none with OK false.
	PromptDone struct {
		Kind, Key, Value string
		OK               bool
	}
)

// promptView is a dialog asking for a line of text.
type promptView struct {
	*widget.Dialog
	field *widget.TextField
}

// show gives the field the keyboard, all of its text chosen, once a menu
// that led here has given the keyboard back.
func (v *promptView) show(_ PromptAsk, u *gunim.UI) {
	u.After(120*time.Millisecond, func(u *gunim.UI) {
		u.Focus(v.field)
		v.field.Select(0, len([]rune(v.field.Text())))
		u.Invalidate()
	})
}

// newPromptDialog is a dialog asking for a line of text.
func newPromptDialog(s PromptAsk) *promptView {
	d := widget.NewDialog(s.Title)
	d.Width = 420
	field := widget.NewTextField()
	field.SetText(s.Value, nil)
	field.Placeholder = s.Label
	label := newSmallLabel(s.Label)
	label.Color = noteInk
	d.Body = widget.Column(label, spacer(4), field)
	d.SetButtons(s.OK, "Cancel")
	d.Check = func() string {
		if strings.TrimSpace(field.Text()) == "" {
			return "Type a name"
		}
		return ""
	}
	d.OnAccept = func(*gunim.UI) gunim.Intent {
		return PromptDone{Kind: s.Kind, Key: s.Key, Value: field.Text(), OK: true}
	}
	d.OnDismiss = widget.Sends(PromptDone{Kind: s.Kind, Key: s.Key})
	return &promptView{Dialog: d, field: field}
}

// askPrompt asks for a line of text with the dialog s says.
func (cu *culler) askPrompt(s PromptAsk) {
	if cu.asking {
		return
	}
	cu.asking = true
	_ = cu.c.Mount(gunim.Root, "prompt", "prompt", s)
	_ = cu.c.Update("prompt", s)
}

// promptDone takes the text given.
func (cu *culler) promptDone(in PromptDone) {
	cu.asking = false
	_ = cu.c.Unmount("prompt")
	cu.refocus()
	if !in.OK {
		return
	}
	switch in.Kind {
	case "rename", "renameDisk":
		cu.railRenamed(in.Kind, in.Key, in.Value)
	}
}
