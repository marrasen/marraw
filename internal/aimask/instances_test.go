package aimask

import "testing"

// blob paints a filled rectangle of probability p into a w×h float mask.
func blob(w, h, x0, y0, x1, y1 int, p float32) []float32 {
	m := make([]float32, w*h)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			m[y*w+x] = p
		}
	}
	return m
}

func TestComposeInstancePlaneArgmaxAndOrder(t *testing.T) {
	const w, h = 100, 80
	// Right-side person first in detection order, left-side second — compose
	// must relabel left-to-right. They overlap in x=45..55 where the right
	// one is more confident.
	right := InstanceMask{Prob: blob(w, h, 45, 10, 90, 70, 0.9), Score: 0.9}
	left := InstanceMask{Prob: blob(w, h, 10, 10, 55, 70, 0.7), Score: 0.8}
	g := ComposeInstancePlane([]InstanceMask{right, left}, w, h)

	if got := g.Pix[40*w+20]; got != 1 {
		t.Errorf("left person pixel = %d, want ID 1", got)
	}
	if got := g.Pix[40*w+80]; got != 2 {
		t.Errorf("right person pixel = %d, want ID 2", got)
	}
	// Overlap goes to the higher-probability instance (the right one).
	if got := g.Pix[40*w+50]; got != 2 {
		t.Errorf("overlap pixel = %d, want ID 2 (argmax)", got)
	}
	if got := g.Pix[0]; got != 0 {
		t.Errorf("background pixel = %d, want 0", got)
	}

	ins := DetectInstances(g.Pix, w, h)
	if len(ins) != 2 {
		t.Fatalf("detected %d instances, want 2: %+v", len(ins), ins)
	}
	if ins[0].ID != 1 || ins[1].ID != 2 || ins[0].CX >= ins[1].CX {
		t.Errorf("instances not left-to-right: %+v", ins)
	}
	if ins[0].Fraction <= 0 || ins[0].Fraction >= 1 {
		t.Errorf("fraction out of range: %+v", ins[0])
	}
}

func TestComposeInstancePlaneFilters(t *testing.T) {
	const w, h = 100, 100
	big := InstanceMask{Prob: blob(w, h, 0, 0, 40, 40, 0.9), Score: 0.9}
	lowScore := InstanceMask{Prob: blob(w, h, 60, 0, 100, 40, 0.9), Score: 0.2}
	sliver := InstanceMask{Prob: blob(w, h, 60, 60, 63, 63, 0.9), Score: 0.9} // 9 px < 0.2%
	lowProb := InstanceMask{Prob: blob(w, h, 0, 60, 40, 100, 0.3), Score: 0.9}
	g := ComposeInstancePlane([]InstanceMask{big, lowScore, sliver, lowProb}, w, h)

	ins := DetectInstances(g.Pix, w, h)
	if len(ins) != 1 || ins[0].ID != 1 {
		t.Fatalf("want only the big instance to survive, got %+v", ins)
	}
	if got := g.Pix[20*w+80]; got != 0 {
		t.Errorf("low-score instance leaked: pixel = %d", got)
	}
	if got := g.Pix[80*w+61]; got != 0 {
		t.Errorf("sliver instance leaked: pixel = %d", got)
	}
	if got := g.Pix[80*w+20]; got != 0 {
		t.Errorf("sub-threshold probability leaked: pixel = %d", got)
	}
}

func TestComposeInstancePlaneCap(t *testing.T) {
	// 40 disjoint columns in a 400×50 plane, strictly shrinking left to
	// right so "the 32 largest" is exactly the leftmost 32 — the cap must
	// keep those and relabel them densely 1..32.
	const w, h = 400, 50
	var insts []InstanceMask
	for i := 0; i < 40; i++ {
		insts = append(insts, InstanceMask{
			Prob:  blob(w, h, i*10, 0, i*10+10, h-i, 0.9),
			Score: 0.9,
		})
	}
	g := ComposeInstancePlane(insts, w, h)
	ins := DetectInstances(g.Pix, w, h)
	if len(ins) != maxInstances {
		t.Fatalf("detected %d instances, want the %d cap", len(ins), maxInstances)
	}
	for i, in := range ins {
		if in.ID != i+1 {
			t.Fatalf("IDs not dense 1..N after cap: %+v", ins)
		}
	}
}

func TestDetectInstancesEmpty(t *testing.T) {
	if got := DetectInstances(make([]uint8, 100), 10, 10); got != nil {
		t.Errorf("empty plane detected %+v", got)
	}
	if got := DetectInstances(nil, 10, 10); got != nil {
		t.Errorf("short plane detected %+v", got)
	}
}
