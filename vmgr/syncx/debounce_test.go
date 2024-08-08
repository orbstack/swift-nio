package syncx

import (
	"testing"
	"time"
)

func TestFuncDebounce(t *testing.T) {
	t.Parallel()

	var count int
	f := NewFuncDebounce(100*time.Millisecond, func() {
		count++
	})

	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(200 * time.Millisecond)

	if count != 1 {
		t.Fatal("expected 1")
	}
}

func TestFuncDebounceCancel(t *testing.T) {
	t.Parallel()

	var count int
	f := NewFuncDebounce(100*time.Millisecond, func() {
		count++
	})

	f.Call()
	time.Sleep(200 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Cancel()
	time.Sleep(200 * time.Millisecond)

	if count != 1 {
		t.Fatal("expected 1")
	}
}
