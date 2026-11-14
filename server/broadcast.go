package server

import (
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/protocol"
)

// Broadcaster is a per-task in-process pub/sub for live activity events. The
// engine publishes each appended activity at its single emit point; SSE handlers
// subscribe per task. Storage (activity table) and the live stream come from the
// same Publish call, so they never diverge.
//
// S1 added a second, additive channel: the harness protocol Envelope stream. It
// carries the same events wrapped in Thread/Turn/Item with the revision attached.
// The legacy channel is kept untouched so the existing frontend keeps working
// unchanged (no behaviour change, no forced migration) — the envelope stream is
// what the harness console and the replay tool consume.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[string]map[chan db.Activity]struct{}       // task id -> legacy activity subscribers
	envs map[string]map[chan protocol.Envelope]struct{} // task id -> envelope subscribers
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: map[string]map[chan db.Activity]struct{}{},
		envs: map[string]map[chan protocol.Envelope]struct{}{},
	}
}

// Subscribe returns a buffered channel of activities for a task plus an
// unsubscribe func the caller must invoke (defer) to release it.
func (b *Broadcaster) Subscribe(task string) (<-chan db.Activity, func()) {
	ch := make(chan db.Activity, 256)
	b.mu.Lock()
	if b.subs[task] == nil {
		b.subs[task] = map[chan db.Activity]struct{}{}
	}
	b.subs[task][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if m := b.subs[task]; m != nil {
				delete(m, ch)
				if len(m) == 0 {
					delete(b.subs, task)
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// SubscribeEnvelopes returns a buffered channel of protocol envelopes for a task
// plus an unsubscribe func. Same lossy-but-catch-uppable contract as Subscribe:
// on overflow the event is dropped and the client resumes from its last revision
// (the DB is the source of truth).
func (b *Broadcaster) SubscribeEnvelopes(task string) (<-chan protocol.Envelope, func()) {
	ch := make(chan protocol.Envelope, 256)
	b.mu.Lock()
	if b.envs[task] == nil {
		b.envs[task] = map[chan protocol.Envelope]struct{}{}
	}
	b.envs[task][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if m := b.envs[task]; m != nil {
				delete(m, ch)
				if len(m) == 0 {
					delete(b.envs, task)
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Publish fans an activity out to all subscribers of a task. Non-blocking: if a
// subscriber's buffer is full the event is dropped — the client reconnects with
// its last seq cursor and catches up the gap from the DB, so liveness never
// stalls the engine.
func (b *Broadcaster) Publish(task string, a db.Activity) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[task] {
		select {
		case ch <- a:
		default:
		}
	}
}

// PublishEnvelope fans one protocol envelope out to its subscribers (S1).
func (b *Broadcaster) PublishEnvelope(task string, env protocol.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.envs[task] {
		select {
		case ch <- env:
		default:
		}
	}
}
