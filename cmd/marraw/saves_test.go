package main

import "testing"

// TestLedgerAppliesNewestAnswer: answers apply by save number, whatever
// order they come in, and a failed latest leaves the earlier answer applied.
func TestLedgerAppliesNewestAnswer(t *testing.T) {
	var l saveLedger
	l.sent(1)
	l.sent(1)
	if !l.answered(1, 2, true) {
		t.Fatal("the newest save's answer was not applied")
	}
	if l.answered(1, 1, true) {
		t.Error("an older save answering after a newer one was applied")
	}
	l.sent(1)
	l.sent(1)
	if !l.answered(1, 3, true) {
		t.Fatal("save 3 answering well was not applied")
	}
	if l.answered(1, 4, false) {
		t.Error("a failed save was applied")
	}
	if l.applied[1] != 3 {
		t.Errorf("applied %d after save 4 failed, want 3", l.applied[1])
	}
}

// TestLedgerHoldsLatestPatch: while own saves are out only the latest patch
// is held, handed back once none is out; with none out nothing is held.
func TestLedgerHoldsLatestPatch(t *testing.T) {
	var l saveLedger
	if l.patch(1, "x") {
		t.Fatal("a patch was held with no own save out")
	}
	l.sent(1)
	l.sent(1)
	if !l.patch(1, "a") || !l.patch(1, "b") {
		t.Fatal("a patch with own saves out was not held")
	}
	l.answered(1, 1, true)
	if _, ok := l.release(1); ok {
		t.Fatal("the patch was handed back with a save still out")
	}
	l.answered(1, 2, true)
	if h, ok := l.release(1); !ok || h != "b" {
		t.Errorf("released %q, %v; want the latest patch b", h, ok)
	}
	if _, ok := l.release(1); ok {
		t.Error("the held patch was handed back twice")
	}
}
