package mcp

import (
	"math"
	"sync"
	"time"
)

// Limits bound what one account, and everyone together, may ask of /mcp.
// Accounts are free (an anonymous one per OAuth consent) and the calls come
// from Anthropic's or OpenAI's servers, so the relay's per-IP limit can't
// tell users apart: these do. Zero values take the defaults.
type Limits struct {
	PerMinute     int // requests a minute per account, refilled evenly (120)
	Burst         int // requests an account may make at once after a quiet spell (40)
	InFlight      int // requests of one account running at the same time (4: the card's 25 s polls next to the model's 90 s get_answers)
	InFlightTotal int // requests running at the same time, all accounts (256)
}

func (l Limits) withDefaults() Limits {
	if l.PerMinute <= 0 {
		l.PerMinute = 120
	}
	if l.Burst <= 0 {
		l.Burst = 40
	}
	if l.InFlight <= 0 {
		l.InFlight = 4
	}
	if l.InFlightTotal <= 0 {
		l.InFlightTotal = 256
	}
	return l
}

// busyRetry is the Retry-After for "too many at once": one running request is
// likely done by then.
const busyRetry = 5 * time.Second

type limiter struct {
	lim       Limits
	rate      float64 // tokens a second
	now       func() time.Time
	total     chan struct{}
	mu        sync.Mutex
	accts     map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens   float64
	at       time.Time
	inflight int
}

func newLimiter(l Limits, now func() time.Time) *limiter {
	l = l.withDefaults()
	return &limiter{lim: l, rate: float64(l.PerMinute) / 60, now: now,
		total: make(chan struct{}, l.InFlightTotal), accts: map[string]*bucket{}}
}

// refill brings b's tokens up to now. Call with mu held.
func (l *limiter) refill(b *bucket, now time.Time) {
	if d := now.Sub(b.at).Seconds(); d > 0 {
		b.tokens = math.Min(float64(l.lim.Burst), b.tokens+d*l.rate)
	}
	b.at = now
}

// wait is how long until b holds n tokens. Call with mu held.
func (l *limiter) wait(b *bucket, n float64) time.Duration {
	d := time.Duration((n - b.tokens) / l.rate * float64(time.Second))
	if d < time.Second {
		d = time.Second
	}
	return d
}

// acquire lets one request of account id in: it takes a token and a running
// slot. ok false: retry tells when to come back. release frees the slot.
func (l *limiter) acquire(id string) (release func(), retry time.Duration, ok bool) {
	select {
	case l.total <- struct{}{}:
	default:
		return nil, busyRetry, false
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	b := l.accts[id]
	if b == nil {
		b = &bucket{tokens: float64(l.lim.Burst), at: now}
		l.accts[id] = b
	}
	l.refill(b, now)
	if b.inflight >= l.lim.InFlight {
		<-l.total
		return nil, busyRetry, false
	}
	if b.tokens < 1 {
		<-l.total
		return nil, l.wait(b, 1), false
	}
	b.tokens--
	b.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			b.inflight--
			l.mu.Unlock()
			<-l.total
		})
	}, 0, true
}

// charge takes n more tokens from account id (the rest of a batch).
func (l *limiter) charge(id string, n int) (retry time.Duration, ok bool) {
	if n <= 0 {
		return 0, true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.accts[id]
	if b == nil {
		return 0, true
	}
	l.refill(b, now)
	if b.tokens < float64(n) {
		return l.wait(b, float64(n)), false
	}
	b.tokens -= float64(n)
	return 0, true
}

// sweep forgets, once a minute, the accounts that are back to a full bucket
// with nothing running: they'd start the same. Call with mu held.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for id, b := range l.accts {
		l.refill(b, now)
		if b.inflight == 0 && b.tokens >= float64(l.lim.Burst) {
			delete(l.accts, id)
		}
	}
}
