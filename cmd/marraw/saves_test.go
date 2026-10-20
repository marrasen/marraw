package main

import (
	"slices"
	"testing"
	"time"
)

// TestLedgerAppliesNewestAnswer: answers apply by save number, whatever
// order they come in, and a failed latest leaves the earlier answer applied.
func TestLedgerAppliesNewestAnswer(t *testing.T) {
	var l saveLedger
	l.sent(1)
	l.sent(1)
	if !l.answered(1, 2, "h2", true) {
		t.Fatal("the newest save's answer was not applied")
	}
	if l.answered(1, 1, "h1", true) {
		t.Error("an older save answering after a newer one was applied")
	}
	l.sent(1)
	l.sent(1)
	if !l.answered(1, 3, "h3", true) {
		t.Fatal("save 3 answering well was not applied")
	}
	if l.answered(1, 4, "", false) {
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
	if l.patch(1, "x") {
		t.Fatal("a patch was held with no own save out")
	}
	l.sent(1)
	l.sent(1)
	if !l.patch(1, "a") || !l.patch(1, "b") {
		t.Fatal("a patch with own saves out was not held")
	}
	l.answered(1, 1, "a", true)
	if h := l.release(1); h != nil {
		t.Fatalf("patches handed back with a save still out: %v", h)
	}
	l.answered(1, 2, "b", true)
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

// judge mirrors culler.judgeHash's rule on the ledger alone, for a patch
// that comes now: own, else an outside edit when the hash is not the
// photo's.
func judge(l *saveLedger, id int64, current, hash string) (outside bool) {
	return judgeAt(l, id, current, hash, l.clock())
}

func judgeAt(l *saveLedger, id int64, current, hash string, came time.Time) (outside bool) {
	return !l.isOwn(id, hash, came) && hash != current
}

// TestAnswersBeforePatches: two quick own saves whose answers both reach
// the loop before their patches — h1's patch, then h2's — are not taken for
// edits made elsewhere, though the photo is on h2 when h1's patch comes.
func TestAnswersBeforePatches(t *testing.T) {
	var l saveLedger
	l.sent(1)
	l.sent(1)
	l.answered(1, 1, "h1", true)
	l.answered(1, 2, "h2", true)
	current := "h2"
	for _, h := range []string{"h1", "h2"} {
		if l.patch(1, h) {
			t.Fatalf("patch %s held with no save out", h)
		}
		if judge(&l, 1, current, h) {
			t.Errorf("own patch %s taken for an edit made elsewhere", h)
		}
	}
}

// TestHeldPatchesJudged: patches held while saves are out are judged in
// order once none is: own ones are nothing, a fresh hash after them is an
// edit made elsewhere.
func TestHeldPatchesJudged(t *testing.T) {
	var l saveLedger
	l.sent(1)
	l.sent(1)
	l.patch(1, "h1")
	l.patch(1, "h2")
	l.patch(1, "x") // another client's edit, after both saves
	l.answered(1, 2, "h2", true)
	l.answered(1, 1, "h1", true)
	current := "h2"
	var outside []string
	for _, h := range l.release(1) {
		if judgeAt(&l, 1, current, h.hash, h.at) {
			outside = append(outside, h.hash)
			current = h.hash
		}
	}
	if !slices.Equal(outside, []string{"x"}) {
		t.Errorf("taken for edits made elsewhere: %v; want [x]", outside)
	}
}

// TestOwnHashesExpire: an edit made elsewhere back to a state an own save
// answered with more than ownHashTTL ago is an edit made elsewhere.
func TestOwnHashesExpire(t *testing.T) {
	now := time.Unix(1000, 0)
	l := saveLedger{now: func() time.Time { return now }}
	l.sent(1)
	l.answered(1, 1, "h1", true)
	l.sent(1)
	l.answered(1, 2, "h2", true)
	if judge(&l, 1, "h2", "h1") {
		t.Fatal("a fresh own hash taken for an edit made elsewhere")
	}
	now = now.Add(ownHashTTL + time.Second)
	if !judge(&l, 1, "h2", "h1") {
		t.Error("an edit made elsewhere back to an old own state was taken for the panel's own")
	}
	if !judge(&l, 1, "h2", "fresh") {
		t.Error("a fresh outside hash was not taken for an edit made elsewhere")
	}
}

// TestHeldPatchJudgedAsOfArrival: an own patch held while a later save of
// the photo sits out longer than ownHashTTL is still the panel's own: it is
// judged as of when it came, not when the last save answered.
func TestHeldPatchJudgedAsOfArrival(t *testing.T) {
	now := time.Unix(1000, 0)
	l := saveLedger{now: func() time.Time { return now }}
	l.sent(1)
	l.answered(1, 1, "h1", true)
	l.sent(1) // save 2, stuck behind a slow save
	now = now.Add(time.Second)
	if !l.patch(1, "h1") {
		t.Fatal("h1's patch not held with save 2 out")
	}
	now = now.Add(ownHashTTL + 10*time.Second)
	l.answered(1, 2, "h2", true)
	for _, h := range l.release(1) {
		if judgeAt(&l, 1, "h2", h.hash, h.at) {
			t.Errorf("own patch %s, held past ownHashTTL, taken for an edit made elsewhere", h.hash)
		}
	}
}

// TestHeldPatchOwnAnswerWhileHeld: a patch that comes before its own save
// answers — held for exactly that — is the panel's own, the answer coming
// after it.
func TestHeldPatchOwnAnswerWhileHeld(t *testing.T) {
	now := time.Unix(1000, 0)
	l := saveLedger{now: func() time.Time { return now }}
	l.sent(1)
	l.patch(1, "h1")
	now = now.Add(2 * time.Second)
	l.answered(1, 1, "h1", true)
	for _, h := range l.release(1) {
		if judgeAt(&l, 1, "h1", h.hash, h.at) || judgeAt(&l, 1, "h0", h.hash, h.at) {
			t.Errorf("own patch %s answered while held taken for an edit made elsewhere", h.hash)
		}
	}
}
