package engine

import (
	"container/heap"
	"sync"
	"time"
)

// Clock provides time operations for deterministic testing
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	Sleep(d time.Duration)
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer represents a cancellable timer
type Timer interface {
	Stop() bool
}

// AutoClock uses real time
type AutoClock struct{}

// NewAutoClock creates a clock that uses real time
func NewAutoClock() *AutoClock {
	return &AutoClock{}
}

func (c *AutoClock) Now() time.Time {
	return time.Now()
}

func (c *AutoClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

func (c *AutoClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

func (c *AutoClock) AfterFunc(d time.Duration, f func()) Timer {
	return &autoTimer{timer: time.AfterFunc(d, f)}
}

type autoTimer struct {
	timer *time.Timer
}

func (t *autoTimer) Stop() bool {
	return t.timer.Stop()
}

// AutoAdvancableClock uses real time but can be advanced forward. Internally it
// keeps a manual clock that is automatically synchronised with wall-clock time
// and can also be advanced programmatically for deterministic testing.
type AutoAdvancableClock struct {
	manual   *ManualClock
	mu       sync.Mutex
	lastReal time.Time
	ticker   *time.Ticker
	stopCh   chan struct{}
	stopOnce sync.Once
}

const autoClockSyncInterval = 10 * time.Millisecond

// NewAutoAdvancableClock creates a clock that follows real time but still
// allows callers to fast-forward time deterministically via Advance.
func NewAutoAdvancableClock() *AutoAdvancableClock {
	now := time.Now()
	clock := &AutoAdvancableClock{
		manual:   NewManualClock(now),
		lastReal: now,
		ticker:   time.NewTicker(autoClockSyncInterval),
		stopCh:   make(chan struct{}),
	}

	go clock.run()

	return clock
}

func (c *AutoAdvancableClock) run() {
	for {
		select {
		case <-c.ticker.C:
			c.syncToRealTime()
		case <-c.stopCh:
			c.ticker.Stop()
			return
		}
	}
}

func (c *AutoAdvancableClock) syncToRealTime() {
	current := time.Now()
	c.mu.Lock()
	delta := current.Sub(c.lastReal)
	if delta > 0 {
		c.lastReal = current
	}
	c.mu.Unlock()

	if delta > 0 {
		c.manual.Advance(delta)
	}
}

// Stop halts the background synchronisation goroutine. It is safe to call
// multiple times.
func (c *AutoAdvancableClock) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

func (c *AutoAdvancableClock) ensureUpToDate() {
	c.syncToRealTime()
}

func (c *AutoAdvancableClock) Now() time.Time {
	c.ensureUpToDate()
	return c.manual.Now()
}

func (c *AutoAdvancableClock) After(d time.Duration) <-chan time.Time {
	c.ensureUpToDate()
	return c.manual.After(d)
}

func (c *AutoAdvancableClock) Sleep(d time.Duration) {
	c.ensureUpToDate()
	c.manual.Sleep(d)
}

func (c *AutoAdvancableClock) AfterFunc(d time.Duration, f func()) Timer {
	c.ensureUpToDate()
	return c.manual.AfterFunc(d, f)
}

// Advance moves the clock forward by advancing the underlying manual clock.
func (c *AutoAdvancableClock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.manual.Advance(d)
}

// ManualClock provides deterministic time control for testing
type ManualClock struct {
	mu      sync.RWMutex
	now     time.Time
	timers  timerHeap
	stopped bool
}

// NewManualClock creates a clock with manual time control
func NewManualClock(start time.Time) *ManualClock {
	if start.IsZero() {
		start = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &ManualClock{
		now:    start,
		timers: make(timerHeap, 0),
	}
}

func (c *ManualClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *ManualClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.AfterFunc(d, func() {
		ch <- c.Now()
	})
	return ch
}

func (c *ManualClock) Sleep(d time.Duration) {
	// In manual mode, sleep is a no-op; time only advances via Advance()
	// But we can wait on a timer
	<-c.After(d)
}

func (c *ManualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return &manualTimer{stopped: true}
	}

	mt := &manualTimer{
		fireAt: c.now.Add(d),
		fn:     f,
		clock:  c,
	}
	heap.Push(&c.timers, mt)
	return mt
}

// Advance moves time forward and fires all timers that should trigger
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
	c.fireDueTimers()
}

// AdvanceTo sets the current time to a specific point
func (c *ManualClock) AdvanceTo(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if t.After(c.now) {
		c.now = t
		c.fireDueTimers()
	}
}

func (c *ManualClock) fireDueTimers() {
	// Fire timers in order until we run out of due timers
	for len(c.timers) > 0 {
		mt := c.timers[0]
		if mt.stopped || mt.fireAt.After(c.now) {
			// If stopped or not yet due, remove stopped ones
			if mt.stopped {
				heap.Pop(&c.timers)
				continue
			}
			break
		}

		// Pop and fire
		heap.Pop(&c.timers)
		if mt.fn != nil && !mt.stopped {
			// Execute callback without holding lock
			c.mu.Unlock()
			mt.fn()
			c.mu.Lock()
		}
	}
}

type manualTimer struct {
	fireAt  time.Time
	fn      func()
	clock   *ManualClock
	stopped bool
	index   int // for heap
}

func (t *manualTimer) Stop() bool {
	if t.stopped {
		return false
	}
	t.stopped = true

	// Remove from heap
	if t.clock != nil {
		t.clock.mu.Lock()
		defer t.clock.mu.Unlock()
		// Mark as stopped; will be cleaned up on next advance
	}
	return true
}

// timerHeap implements heap.Interface for timers
type timerHeap []*manualTimer

func (h timerHeap) Len() int           { return len(h) }
func (h timerHeap) Less(i, j int) bool { return h[i].fireAt.Before(h[j].fireAt) }
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *timerHeap) Push(x any) {
	n := len(*h)
	timer := x.(*manualTimer)
	timer.index = n
	*h = append(*h, timer)
}

func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	timer := old[n-1]
	old[n-1] = nil
	timer.index = -1
	*h = old[0 : n-1]
	return timer
}
