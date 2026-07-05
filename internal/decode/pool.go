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
	key     string
	prio    Priority
	seq     uint64 // FIFO tiebreak within a priority
	fn      func(ctx context.Context, p *libraw.Processor) error
	done    chan struct{}
	err     error
	index     int // heap index; -1 once dequeued
	waiters   int // Do calls currently waiting on this job
	abandoned bool
	ctx       context.Context
	cancel    context.CancelFunc
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
// its priority if prio is higher). When every waiter's ctx is canceled the
// job is abandoned: dropped from the queue if it hasn't started, or its fn
// ctx canceled if it has — so scanning quickly past photos doesn't leave a
// backlog of full renders grinding the CPU.
func (p *Pool) Do(ctx context.Context, key string, prio Priority, fn func(ctx context.Context, proc *libraw.Processor) error) error {
retry:
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return context.Canceled
	}
	j, ok := p.inflight[key]
	if ok && j.abandoned {
		// The running job is doomed (its ctx canceled after every waiter
		// left); joining it would surface a cancellation this caller never
		// asked for. Wait it out, then start fresh.
		p.mu.Unlock()
		select {
		case <-j.done:
			goto retry
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if ok {
		// Promote a queued duplicate so the waiter isn't stuck behind
		// background work.
		if j.index >= 0 && prio > j.prio {
			j.prio = prio
			heap.Fix(&p.queue, j.index)
		}
	} else {
		p.seq++
		jctx, jcancel := context.WithCancel(context.Background())
		j = &job{key: key, prio: prio, seq: p.seq, fn: fn, done: make(chan struct{}), ctx: jctx, cancel: jcancel}
		p.inflight[key] = j
		heap.Push(&p.queue, j)
		p.cond.Signal()
	}
	j.waiters++
	p.mu.Unlock()

	select {
	case <-j.done:
		return j.err
	case <-ctx.Done():
		p.abandon(j)
		return ctx.Err()
	}
}

// abandon releases one waiter. The last waiter to give up kills the job:
// still-queued jobs are removed outright; a running job gets its fn ctx
// canceled so the render bails at the next checkpoint.
func (p *Pool) abandon(j *job) {
	p.mu.Lock()
	j.waiters--
	if j.waiters > 0 {
		p.mu.Unlock()
		return
	}
	if j.index >= 0 {
		heap.Remove(&p.queue, j.index)
		delete(p.inflight, j.key)
		j.err = context.Canceled
		p.mu.Unlock()
		j.cancel()
		close(j.done)
		return
	}
	j.abandoned = true
	p.mu.Unlock()
	j.cancel()
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

		j.err = j.fn(j.ctx, proc)
		proc.Recycle()
		j.cancel()

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
