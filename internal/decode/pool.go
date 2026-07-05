// Package decode runs LibRaw work on a bounded pool of worker goroutines,
// each owning one long-lived Processor handle.
package decode

import (
	"container/heap"
	"context"
	"sync"

	"github.com/marrasen/marraw/internal/libraw"
)

type Priority int

const (
	PriorityBackground Priority = iota // scan-time metadata + thumbs
	PriorityPrefetch                   // near-viewport prefetch
	PriorityVisible                    // client is waiting on this image
	PriorityInteractive                // edit preview
)

type job struct {
	key   string
	prio  Priority
	seq   uint64 // FIFO tiebreak within a priority
	fn    func(p *libraw.Processor) error
	done  chan struct{}
	err   error
	index int // heap index; -1 once dequeued
}

type Pool struct {
	mu       sync.Mutex
	cond     *sync.Cond
	queue    jobHeap
	inflight map[string]*job // queued or running, by dedup key
	seq      uint64
	closed   bool
	wg       sync.WaitGroup
}

func NewPool(workers int) *Pool {
	p := &Pool{inflight: make(map[string]*job)}
	p.cond = sync.NewCond(&p.mu)
	for range workers {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// Do runs fn on a pool worker, deduplicated by key: if a job with the same
// key is already queued or running, Do waits for that job instead (raising
// its priority if prio is higher). Jobs always run to completion — the cache
// file they produce stays useful — but Do returns early with ctx's error if
// the caller gives up waiting.
func (p *Pool) Do(ctx context.Context, key string, prio Priority, fn func(proc *libraw.Processor) error) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return context.Canceled
	}
	j, ok := p.inflight[key]
	if ok {
		// Promote a queued duplicate so the waiter isn't stuck behind
		// background work.
		if j.index >= 0 && prio > j.prio {
			j.prio = prio
			heap.Fix(&p.queue, j.index)
		}
	} else {
		p.seq++
		j = &job{key: key, prio: prio, seq: p.seq, fn: fn, done: make(chan struct{})}
		p.inflight[key] = j
		heap.Push(&p.queue, j)
		p.cond.Signal()
	}
	p.mu.Unlock()

	select {
	case <-j.done:
		return j.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pool) worker() {
	defer p.wg.Done()
	proc, err := libraw.New()
	if err != nil {
		return
	}
	defer proc.Close()
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.closed {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return
		}
		j := heap.Pop(&p.queue).(*job)
		p.mu.Unlock()

		j.err = j.fn(proc)
		proc.Recycle()

		p.mu.Lock()
		delete(p.inflight, j.key)
		p.mu.Unlock()
		close(j.done)
	}
}

func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	p.wg.Wait()
}

type jobHeap []*job

func (h jobHeap) Len() int { return len(h) }
func (h jobHeap) Less(i, j int) bool {
	if h[i].prio != h[j].prio {
		return h[i].prio > h[j].prio
	}
	return h[i].seq < h[j].seq
}
func (h jobHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *jobHeap) Push(x any) {
	j := x.(*job)
	j.index = len(*h)
	*h = append(*h, j)
}
func (h *jobHeap) Pop() any {
	old := *h
	n := len(old)
	j := old[n-1]
	old[n-1] = nil
	j.index = -1
	*h = old[:n-1]
	return j
}
