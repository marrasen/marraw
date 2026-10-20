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

// TestLedgerJudgesByNumber: a patch numbered at or below the newest own
// save that answered well is never outside — a late own patch, held or not;
// one numbered above it, or with no number, is outside when its hash
// differs, an outside edit back to a hash an own save once had included.
func TestLedgerJudgesByNumber(t *testing.T) {
	var l saveLedger
	// Two quick own saves answer before either patch comes.
	l.sent(1)
	l.sent(1)
	l.answered(1, 1, 101, true)
	l.answered(1, 2, 102, true)
	if l.outside(1, "h1", 101, "h2") || l.outside(1, "h2", 102, "h2") {
		t.Error("a late patch of an own save was judged outside")
	}
	// A reset made elsewhere (the grid, another client) to the cleared
	// edit's hash, which an own save of the panel had too: a later number.
	if !l.outside(1, "base", 250, "h2") {
		t.Error("an outside save numbered after the own ones was not judged outside")
	}
	// A patch with no number is judged by its hash.
	if !l.outside(1, "x", 0, "h2") || l.outside(1, "h2", 0, "h2") {
		t.Error("a patch without a save number was not judged by its hash")
	}
	// Another photo's numbers are not this one's.
	if !l.outside(2, "y", 101, "h0") {
		t.Error("an own save of another photo counted for this one")
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

// TestManyOwnSavesLatePatch: a late own patch after many own saves is
// still ignored, being at or below the newest own save.
func TestManyOwnSavesLatePatch(t *testing.T) {
	p := ledgerPhoto{hash: "h0"}
	for i := 1; i <= 40; i++ {
		p.l.sent(1)
		p.answer(i, int64(1000+i), "h")
	}
	p.patch("old", 1001)
	if len(p.reloads) != 0 {
		t.Errorf("a late own patch reloaded: %v", p.reloads)
	}
}
