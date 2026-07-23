package zephyr

import (
	"context"
	"sync"
)

// lifecycle coordinates the run-once-at-a-time blocking lifecycles of Gateway
// and Service: exclusive entry, interruption via Close, completion signaling,
// and a per-run readiness channel.
type lifecycle struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
}

// begin claims the lifecycle, returning alreadyErr if it is already claimed.
// The returned context is canceled by interrupt (Close) or the parent.
func (l *lifecycle) begin(ctx context.Context, alreadyErr error) (context.Context, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		return nil, alreadyErr
	}
	ctx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.done = make(chan struct{})
	if l.ready == nil {
		l.ready = make(chan struct{})
	}
	return ctx, nil
}

// markReady closes the current run's ready channel.
func (l *lifecycle) markReady() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ready == nil {
		l.ready = make(chan struct{})
	}
	select {
	case <-l.ready:
	default:
		close(l.ready)
	}
}

// readyChan returns the current run's ready channel, creating it if needed so
// callers may wait before the run begins.
func (l *lifecycle) readyChan() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ready == nil {
		l.ready = make(chan struct{})
	}
	return l.ready
}

// interrupt cancels the current run without waiting for it to finish. It is a
// no-op when no run is active.
func (l *lifecycle) interrupt() {
	l.mu.Lock()
	cancel := l.cancel
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// end releases the lifecycle and wakes anyone blocked in close.
func (l *lifecycle) end() {
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.cancel = nil
	l.done = nil
	l.ready = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		close(done)
	}
}

// close interrupts the current run and waits for it to end. It is a no-op
// when no run is active and is safe to call multiple times.
func (l *lifecycle) close() {
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
