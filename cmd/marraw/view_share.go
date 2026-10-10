package main

import (
	"image/color"
	"net/url"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// shareHours are how long a link can live, nought for ever.
var (
	shareHours   = []int{4, 24, 168, 0}
	shareHoursAs = []string{"4 hours", "1 day", "7 days", "Never"}
	shareWarnInk = theme.Color("marraw.share.warn", color.NRGBA{R: 0xf0, G: 0xb0, B: 0x50, A: 0xff})
)

// shareView is the share dialog: who can open the link, what they can
// do, how long it lives, and whether this computer can be reached; then
// the link, copied.
type shareView struct {
	*widget.Dialog
	st                    ShareState
	reach                 *widget.Segmented
	reachNote             *widget.Label
	cull, edits, download *widget.Switch
	preset                *widget.Dropdown
	presetIDs             []string
	presetNote            *widget.Label
	presetFold            *widget.Fold
	expiry                *widget.Segmented
	note                  *widget.Label
	form                  *widget.Fold
	// consented says the warning that the link publishes this computer
	// has been shown, so the next press makes it.
	consented bool
}

func newShareView(s ShareState) *shareView {
	d := widget.NewDialog("Share “" + s.Name + "”")
	d.Width = 520
	v := &shareView{Dialog: d, st: s}
	about := newSmallLabel("Send a link that opens this album in a browser.")
	about.Color = noteInk
	v.reach = widget.NewSegmented("Anyone with the link", "Only my devices")
	v.reach.KeepFocus = true
	v.reach.OnChange = func(int, *gunim.UI) gunim.Intent {
		v.consented = false
		v.showNotes()
		return nil
	}
	v.reachNote = newSmallLabel("")
	v.reachNote.Color, v.reachNote.MaxLines = noteInk, 3
	capability := func(label, hint string, on bool) (*widget.Switch, gunim.Node) {
		sw := widget.NewSwitch(label)
		sw.KeepFocus = true
		sw.SetChecked(on, nil)
		h := newSmallLabel(hint)
		h.Color, h.MaxLines = noteInk, 2
		pad := widget.NewPad(h)
		pad.Padding = theme.Insets("marraw.share.hint", geom.Insets{Left: 48})
		return sw, widget.Column(sw, pad)
	}
	var cullRow, editsRow, downRow gunim.Node
	v.cull, cullRow = capability("Rate and pick", "They can star and pick photos. Their choices show up in your library.", true)
	v.edits, editsRow = capability("Show my edits", "Off shows the photos straight out of the camera.", true)
	v.download, downRow = capability("Allow downloads", "They can save JPEGs of any photo.", false)
	v.download.OnChange = func(on bool, u *gunim.UI) gunim.Intent {
		v.presetFold.SetOpen(on, u)
		return nil
	}
	v.preset = widget.NewDropdown(nil)
	v.preset.KeepFocus = true
	v.preset.OnChange = func(int, *gunim.UI) gunim.Intent {
		v.showPreset()
		return nil
	}
	v.presetNote = newSmallLabel("")
	v.presetNote.Color, v.presetNote.MaxLines = noteInk, 3
	v.presetFold = widget.NewFold(widget.Column(spacer(6), wmLine("Download size", &fixedWidth{w: 220, child: v.preset}), v.presetNote), false)
	v.expiry = widget.NewSegmented(shareHoursAs...)
	v.expiry.KeepFocus = true
	v.expiry.SetSelected(1, nil)
	v.note = newSmallLabel("")
	v.note.MaxLines = 5
	v.form = widget.NewFold(widget.Column(
		sectionLabel("Who can open it"), glassSegmented(v.reach), spacer(4), v.reachNote, spacer(10),
		cullRow, spacer(6), editsRow, spacer(6), downRow, v.presetFold, spacer(10),
		wmLine("Expires after", glassSegmented(v.expiry)), spacer(10), v.note), true)

	d.Body = widget.Column(about, spacer(10), v.form)
	d.SetButtons("Create link", "Cancel")
	d.Check = func() string {
		switch {
		case v.base() == "":
			return "There is nowhere to serve the link from yet"
		case v.needsConsent() && !v.consented:
			v.consented = true
			v.showNotes()
			return "Read the warning above, then press Publish and create link"
		}
		return ""
	}
	d.OnAccept = func(u *gunim.UI) gunim.Intent { return v.createLink(u) }
	d.OnDismiss = widget.Sends(ShareDone{})
	return v
}

// show takes s: the reachability as it changes, and the link once made,
// copied as it comes, since sending it is what the dialog is for.
func (v *shareView) show(s ShareState, u *gunim.UI) {
	v.st = s
	items := []widget.MenuItem{{Label: "Full size"}}
	ids := []string{""}
	for _, p := range s.Presets {
		items = append(items, widget.MenuItem{Label: p.Name})
		ids = append(ids, p.ID)
	}
	cur := ""
	if i := v.preset.Selected(); i > 0 && i < len(v.presetIDs) {
		cur = v.presetIDs[i]
	}
	v.presetIDs = ids
	v.preset.SetItems(items)
	v.preset.SetSelected(max(0, indexOf(ids, cur)), u)
	v.showPreset()
	v.showNotes()
	u.Invalidate()
}

// reachKey is the reach chosen.
func (v *shareView) reachKey() marrawclient.ShareReach {
	if v.reach.Selected() == 1 {
		return marrawclient.ShareReachTailnet
	}
	return marrawclient.ShareReachPublic
}

// base is where a link of the reach chosen would be served, or "".
func (v *shareView) base() string {
	st := v.st.Status
	if st == nil {
		return ""
	}
	if v.reachKey() == marrawclient.ShareReachTailnet {
		return st.TailnetBase
	}
	return st.Base
}

// needsConsent says the link would publish this computer to the internet
// for the first time.
func (v *shareView) needsConsent() bool {
	st := v.st.Status
	return v.reachKey() == marrawclient.ShareReachPublic && st != nil && st.Available && !st.Running
}

// createLink makes the link, the first press warning first where it
// would publish this computer.
func (v *shareView) createLink(*gunim.UI) gunim.Intent {
	in := ShareCreate{Caps: marrawclient.GuestCaps{Cull: v.cull.Checked(), Edits: v.edits.Checked(), Downloads: v.download.Checked()},
		Hours: shareHours[max(0, v.expiry.Selected())], Reach: v.reachKey()}
	if in.Caps.Downloads {
		if i := v.preset.Selected(); i > 0 && i < len(v.presetIDs) {
			in.PresetID = v.presetIDs[i]
		}
	}
	return in
}

// showPreset says what a guest's downloads will be.
func (v *shareView) showPreset() {
	v.presetNote.Text = "Full resolution, quality 92."
	i := v.preset.Selected()
	if i <= 0 || i >= len(v.presetIDs) {
		return
	}
	v.presetNote.Text = "As the preset is now: later changes to it do not change this link."
}

// showNotes says who the reach chosen is for, and whether and how this
// computer can be reached.
func (v *shareView) showNotes() {
	tail := v.reachKey() == marrawclient.ShareReachTailnet
	v.reachNote.Text = "Published on the public internet over Tailscale Funnel, so anyone you send it to can open it, with no account and no app."
	if tail {
		v.reachNote.Text = "Devices signed in to your Tailscale network. This computer is never published to the internet."
	}
	warn, text := false, ""
	st := v.st.Status
	switch {
	case st == nil:
		text = "Checking whether this computer can be reached…"
	case v.needsConsent() && v.consented:
		warn = true
		text = "This publishes your computer on the public internet as " + st.Hostname + ". Guests reach only this album, through a link that expires and that you can withdraw at any time, and the tunnel comes down with the last public link. Beyond that there are no guarantees: the link is the whole key, so treat it like one."
	case tail && st.TailnetBase == "":
		warn = true
		text = "This computer is not on a Tailscale network, so there is nothing to serve a devices-only link from. Start Tailscale, or share it with anyone instead."
	case tail:
		text = "Served on your tailnet at " + hostOf(st.TailnetBase) + ", and not published to the internet."
	case st.Base == "":
		warn = true
		lead := "Tailscale isn't running, "
		if st.Err != "" {
			lead = "Tailscale could not publish this computer: " + st.Err + " "
		}
		text = lead + "and this computer has no other address anyone else can reach. Start Tailscale, or allow remote connections in Settings to share over your local network."
	case st.Err != "":
		warn = true
		text = "Tailscale could not publish this computer: " + st.Err + " The link still works where this computer can be reached, at " + hostOf(st.Base) + "."
	case !st.Available:
		warn = true
		text = "Tailscale isn't running, so the link only works on your local network, at " + hostOf(st.Base) + ". Start Tailscale and turn on Funnel to share it over the internet."
	case !st.Running:
		text = "Will publish from " + st.Hostname + " over Tailscale Funnel when you create the link."
	default:
		text = "Served from " + st.Hostname + " over Tailscale Funnel."
	}
	v.note.Text = text
	v.note.Color = noteInk
	if warn {
		v.note.Color = shareWarnInk
	}
	v.SetButtons("Create link", "Cancel")
	if v.needsConsent() && v.consented {
		v.SetButtons("Publish and create link", "Cancel")
	}
}

// hostOf is the host and port of an origin.
func hostOf(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return base
}

// shareLinkView shows the link made, copied already, as sending it is
// what sharing was for.
type shareLinkView struct {
	*widget.Dialog
	link   *marrawclient.ShareLink
	copied *widget.Button
	done   bool
}

func newShareLinkView(s ShareState) *shareLinkView {
	l := s.Link
	d := widget.NewDialog("“" + s.Name + "” is shared")
	d.Width = 520
	v := &shareLinkView{Dialog: d, link: l}
	url := widget.NewLabel(l.URL)
	url.Face, url.MaxLines, url.Selectable = widget.MonoFont, 3, true
	if l.URL == "" {
		url.Text = "This computer cannot be reached from anywhere right now, so there is no link to copy yet. It shows up in Settings, under Sharing, once it can."
		url.Face = widget.Font
	}
	v.copied = widget.NewButton("Copy")
	v.copied.Icon = icon.Copy
	v.copied.OnClick = func(u *gunim.UI) gunim.Intent {
		if l.URL == "" {
			return nil
		}
		u.SetClipboard(l.URL)
		v.copied.Label, v.copied.Icon = "Copied", icon.Check
		return Notify{Text: "Link copied"}
	}
	exp := "It does not expire."
	if l.ExpiresAt > 0 {
		exp = "It expires " + time.UnixMilli(l.ExpiresAt).Format("Mon 2 Jan at 15:04") + "."
	}
	expires := newSmallLabel(exp + " You can withdraw it any time from the shoot's menu in the library, or in Settings.")
	expires.Color, expires.MaxLines = noteInk, 3
	d.Body = widget.Column(&urlBox{child: url}, spacer(8), &alignEnd{child: glassButton(v.copied)}, spacer(6), expires)
	d.SetButtons("Done", "")
	d.OnAccept = widget.Sends(ShareDone{})
	d.OnDismiss = widget.Sends(ShareDone{})
	return v
}

// show copies the link as the dialog opens.
func (v *shareLinkView) show(_ ShareState, u *gunim.UI) {
	if v.done || v.link.URL == "" {
		return
	}
	v.done = true
	u.SetClipboard(v.link.URL)
	v.copied.Label, v.copied.Icon = "Copied", icon.Check
	u.Invalidate()
}

// alignEnd puts its child against the right end of the row.
type alignEnd struct{ child gunim.Node }

// Children implements [gunim.Composite].
func (a *alignEnd) Children() []gunim.Node { return []gunim.Node{a.child} }

// Layout implements [gunim.Node].
func (a *alignEnd) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s := kids.At(0).Layout(gunim.Loose(c.Max))
	w := c.Max.W
	if w <= 0 {
		w = s.W
	}
	kids.At(0).Place(geom.Pt(w-s.W, 0))
	return geom.Sz(w, s.H)
}

// Paint implements [gunim.Node].
func (a *alignEnd) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// urlBox is the link on a dark ground.
type urlBox struct{ child gunim.Node }

// Children implements [gunim.Composite].
func (b *urlBox) Children() []gunim.Node { return []gunim.Node{b.child} }

// Layout implements [gunim.Node].
func (b *urlBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	const pad = 12
	s := kids.At(0).Layout(gunim.Loose(geom.Sz(max(0, c.Max.W-2*pad), 200)))
	kids.At(0).Place(geom.Pt(pad, pad))
	return geom.Sz(c.Max.W, s.H+2*pad)
}

// Paint implements [gunim.Node].
func (b *urlBox) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 8, paint.Solid(color.NRGBA{R: 0x0c, G: 0x0e, B: 0x12, A: 0xff}))
	kids.At(0).Paint(p)
}
