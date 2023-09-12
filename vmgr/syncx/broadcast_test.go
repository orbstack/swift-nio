package syncx

import "testing"

func TestBroadcast(t *testing.T) {
	t.Parallel()

	b := NewBroadcaster[int]()
	defer b.Close()

	ch1 := b.Subscribe()
	ch2 := b.Subscribe()
	ch3 := b.Subscribe()

	b.EmitQueued(1)

	if <-ch1 != 1 {
		t.Error("expected 1")
	}
	if <-ch2 != 1 {
		t.Error("expected 1")
	}
	if <-ch3 != 1 {
		t.Error("expected 1")
	}

	b.Unsubscribe(ch2)
	b.EmitQueued(2)

	if <-ch1 != 2 {
		t.Error("expected 2")
	}
	if <-ch3 != 2 {
		t.Error("expected 2")
	}

	b.Unsubscribe(ch1)
	b.Unsubscribe(ch3)
	b.EmitQueued(3)

	if _, ok := <-ch1; ok {
		t.Error("expected closed channel")
	}
	if _, ok := <-ch2; ok {
		t.Error("expected closed channel")
	}
	if _, ok := <-ch3; ok {
		t.Error("expected closed channel")
	}
}

func TestBroadcastClose(t *testing.T) {
	t.Parallel()

	b := NewBroadcaster[int]()
	ch := b.Subscribe()

	b.Close()

	if _, ok := <-ch; ok {
		t.Error("expected closed channel")
	}
}
