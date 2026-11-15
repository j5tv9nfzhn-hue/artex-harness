package compaction

import "testing"

// A 32K profile is the smallest window the platform allows (agent.minWindowK).
// With the raw subtraction the 20K summary reserve plus the 13K buffer left
// autoThreshold at -1000: over threshold from the first step, forever, while a
// measured run grew to 103010 tokens with zero compactions. These thresholds must
// stay positive and ordered for every window we actually offer.
func TestThresholdsStayPositiveForSmallWindows(t *testing.T) {
	for _, window := range []int{32000, 48000, 64000, 128000, 200000, 1000000} {
		c := New(Config{ContextWindow: window}, nil)
		ew, buf := c.effectiveWindow(), c.buffer()
		if ew <= 0 || ew > window {
			t.Fatalf("window %d: effectiveWindow %d out of range", window, ew)
		}
		if buf <= 0 || buf >= ew {
			t.Fatalf("window %d: buffer %d must be inside (0, effectiveWindow %d)", window, buf, ew)
		}
		at := c.autoThreshold()
		if at <= 0 {
			t.Fatalf("window %d: autoThreshold %d must be positive", window, at)
		}
		if at <= c.microThreshold() {
			t.Fatalf("window %d: micro (%d) must trigger before auto (%d)", window, c.microThreshold(), at)
		}
	}
}

// Large windows keep the upstream tiering: 200K - 20K reserve = 180K, buffer 13K.
func TestLargeWindowKeepsUpstreamBuffer(t *testing.T) {
	c := New(Config{ContextWindow: 200000}, nil)
	if got, want := c.effectiveWindow(), 200000-c.reservedForSummary(); got != want {
		t.Fatalf("effectiveWindow=%d want %d", got, want)
	}
	if got, want := c.buffer(), 13000; got != want {
		t.Fatalf("buffer=%d want %d", got, want)
	}
}
