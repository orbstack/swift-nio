package simplerate

import (
	"testing"
	"time"
)

func TestLimiterAllow(t *testing.T) {
	t.Parallel()

	// create a limiter that allows 2 events per second
	limiter := NewLimiter(2, time.Second)

	// allow 2 events
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}

	// disallow 1 event
	if limiter.Allow() {
		t.Fatal("expected limiter to disallow event")
	}
}

func TestLimiterAllowPeriod(t *testing.T) {
	t.Parallel()

	// create a limiter that allows 2 events per second
	limiter := NewLimiter(2, time.Second)

	// allow 2 events
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}

	// wait 1 second
	time.Sleep(1005 * time.Millisecond)

	// allow 2 events
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
}

func TestLimiterAllowPeriodOverflow(t *testing.T) {
	t.Parallel()

	// create a limiter that allows 2 events per second
	limiter := NewLimiter(2, time.Second)

	// allow 2 events
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}

	// wait 1 second
	time.Sleep(1005 * time.Millisecond)

	// allow 2 events
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if limiter.Allow() {
		t.Fatal("expected limiter to disallow event")
	}

	// wait 1 second
	time.Sleep(1005 * time.Millisecond)

	// allow 2 events
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
	if !limiter.Allow() {
		t.Fatal("expected limiter to allow event")
	}
}
