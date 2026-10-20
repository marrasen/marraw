package main

import (
	"slices"
	"testing"
)

// TestLedgerAppliesNewestAnswer: answers apply by save number, whatever
// order they come in, and a failed latest leaves the earlier answer applied.
func TestLedgerAppliesNewestAnswer(t *testing.T) {
	var l saveLedger
	l.sent(1)
	l.sent(1)
	if !l.answered(1, 2, 102, true) {
		t.Fatal("the newest save's answer was not applied")
	}
	if l.answered(1, 1, 101, true) {
		t.Error("an older save answering after a newer one was applied")
	}
	l.sent(1)
	l.sent(1)
	if !l.answered(1, 3, 103, true) {
		t.Fatal("save 3 answering well was not applied")
	}
	if l.answered(1, 4, 0, false) {
		t.Error("a failed save was applied")
	}
	if l.applied[1] != 3 {
		t.Errorf("applied %d after save 4 failed, want 3", l.applied[1])
	}
}

// TestLedgerHoldsPatchesInOrder: while own saves are out every patch is
// held, handed back in the order they came once none is out; with none out
// nothing is held.
func TestLedgerHoldsPatchesInOrder(t *testing.T) {
	var l saveLedger
	if l.patch(1, "x", 50) {
		t.Fatal("a patch was held with no own save out")
	}
	l.sent(1)
	l.sent(1)
	if !l.patch(1, "a", 101) || !l.patch(1, "b", 102) {
		t.Fatal("a patch with own saves out was not held")
	}
	l.answered(1, 1, 101, true)
	if h := l.release(1); h != nil {
		t.Fatalf("patches handed back with a save still out: %v", h)
	}
	l.answered(1, 2, 102, true)
	var got []string
	for _, p := range l.release(1) {
		got = append(got, p.hash)
	}
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("released %v; want [a b] in order", got)
	}
	if h := l.release(1); h != nil {
		t.Error("held patches were handed back twice")
	}
}

// TestLedgerJudgesByNumber: a patch is the panel's own exactly when its save
// number is one an own save answered with — held or late, and however long
// after; an outside edit back to a hash an own save once had is not.
func TestLedgerJudgesByNumber(t *testing.T) {
	var l saveLedger
	// Two quick own saves answer before either patch comes.
	l.sent(1)
	l.sent(1)
	l.answered(1, 1, 101, true)
	l.answered(1, 2, 102, true)
	if !l.isOwn(1, 101) || !l.isOwn(1, 102) {
		t.Error("a late patch of an own save was not taken as the panel's own")
	}
	// A reset made elsewhere (the grid, another client) to the cleared
	// edit's hash, which an own save of the panel had too: another number.
	if l.isOwn(1, 250) {
		t.Error("an outside save was taken as the panel's own")
	}
	// A patch with no number is never the panel's own by number.
	if l.isOwn(1, 0) {
		t.Error("a patch without a save number was taken as the panel's own")
	}
	// Another photo's numbers are not this one's.
	if l.isOwn(2, 101) {
		t.Error("an own save of another photo counted for this one")
	}
}

// TestLedgerBoundsOwnNumbers: the numbers kept per photo are bounded, the
// newest kept.
func TestLedgerBoundsOwnNumbers(t *testing.T) {
	var l saveLedger
	for i := 1; i <= ownCap+5; i++ {
		l.sent(1)
		l.answered(1, i, int64(1000+i), true)
	}
	if n := len(l.own[1]); n != ownCap {
		t.Fatalf("kept %d numbers, want %d", n, ownCap)
	}
	if !l.isOwn(1, int64(1000+ownCap+5)) || l.isOwn(1, 1001) {
		t.Error("the bound kept the oldest numbers instead of the newest")
	}
}

// ledgerPhoto plays a photo through the ledger the way sendOwn, patchHash
// and judgeHash do: answers set the hash by the ledger's rule, patches are
// held while own saves are out and judged in order once none is, and an
// outside one sets the hash and counts as a reload.
type ledgerPhoto struct {
	l       saveLedger
	hash    string
	reloads []string
}

func (p *ledgerPhoto) answer(seq int, save int64, hash string) {
	if p.l.answered(1, seq, save, true) {
		p.hash = hash
	}
	for _, h := range p.l.release(1) {
		p.judge(h.hash, h.save)
	}
}

func (p *ledgerPhoto) patch(hash string, save int64) {
	if !p.l.patch(1, hash, save) {
		p.judge(hash, save)
	}
}

func (p *ledgerPhoto) judge(hash string, save int64) {
	if p.l.outside(1, hash, save, p.hash) {
		p.hash = hash
		p.reloads = append(p.reloads, hash)
	}
}

// TestHeldOutsideThenOwn: a seed patch (outside, 5) and the panel's own
// save (6) held while the save is out end on the own save's hash, with no
// reload: the seed came before the save that overwrote it.
func TestHeldOutsideThenOwn(t *testing.T) {
	p := ledgerPhoto{hash: "h0"}
	p.l.sent(1)
	p.patch("seed", 5)
	p.patch("h6", 6)
	p.answer(1, 6, "h6")
	if p.hash != "h6" || len(p.reloads) != 0 {
		t.Errorf("ended on %q with reloads %v; want h6 and none", p.hash, p.reloads)
	}
}

// TestLateOwnPatches: two own saves answer before either patch comes; the
// patches then change nothing.
func TestLateOwnPatches(t *testing.T) {
	p := ledgerPhoto{hash: "h0"}
	p.l.sent(1)
	p.l.sent(1)
	p.answer(1, 101, "h1")
	p.answer(2, 102, "h2")
	p.patch("h1", 101)
	p.patch("h2", 102)
	if p.hash != "h2" || len(p.reloads) != 0 {
		t.Errorf("ended on %q with reloads %v; want h2 and none", p.hash, p.reloads)
	}
}

// TestStaleOutsidePatch: an outside patch numbered below the newest own
// save that answered (a seed arriving late) is ignored; one numbered above
// is an edit made elsewhere.
func TestStaleOutsidePatch(t *testing.T) {
	p := ledgerPhoto{hash: "h0"}
	p.l.sent(1)
	p.answer(1, 100, "h1")
	p.patch("seed", 90)
	if p.hash != "h1" || len(p.reloads) != 0 {
		t.Errorf("a stale outside patch moved the photo to %q (reloads %v)", p.hash, p.reloads)
	}
	p.patch("pasted", 120)
	if p.hash != "pasted" || len(p.reloads) != 1 {
		t.Errorf("a fresh outside patch: hash %q, reloads %v; want pasted, one reload", p.hash, p.reloads)
	}
}

// TestOwnNumberOutOfWindow: a late own patch whose number fell out of the
// bounded list is still ignored, being older than the newest own save.
func TestOwnNumberOutOfWindow(t *testing.T) {
	p := ledgerPhoto{hash: "h0"}
	for i := 1; i <= ownCap+5; i++ {
		p.l.sent(1)
		p.answer(i, int64(1000+i), "h")
	}
	p.patch("old", 1001)
	if len(p.reloads) != 0 {
		t.Errorf("a late own patch out of the window reloaded: %v", p.reloads)
	}
}
