package syncx

import (
	"testing"
	"time"
)

func TestLeadingFuncDebounce(t *testing.T) {
	t.Parallel()

	var count int
	debounced := NewLeadingFuncDebounce(100*time.Millisecond, func() {
		count++
	})

	debounced.Call()
	debounced.Call()
	debounced.Call()

	time.Sleep(200 * time.Millisecond)

	if count != 2 {
		t.Fatal("expected count to be 2")
	}

	debounced.Call()
	time.Sleep(200 * time.Millisecond)

	if count != 3 {
		t.Fatal("expected count to be 3; got", count)
	}

	debounced.Call()
	debounced.Call()
	time.Sleep(50 * time.Millisecond)

	if count != 4 {
		t.Fatal("expected count to be 4")
	}
}

func TestLeadingFuncDebounceSlow(t *testing.T) {
	// slow func
	t.Parallel()

	var count int
	debounced := NewLeadingFuncDebounce(100*time.Millisecond, func() {
		time.Sleep(200 * time.Millisecond)
		count++
	})

	debounced.Call()
	debounced.Call()
	debounced.Call()

	time.Sleep(300 * time.Millisecond)

	if count != 1 {
		t.Fatal("expected count to be 1")
	}

	for i := 0; i < 10; i++ {
		before := time.Now()
		debounced.Call()
		if time.Since(before) > 100*time.Millisecond {
			t.Fatal("expected trigger to be fast")
		}
	}

	time.Sleep(400 * time.Millisecond)
	if count != 3 {
		t.Fatalf("expected count to be 3; got %d", count)
	}
}

func TestLeadingFuncDebounceRealWorld(t *testing.T) {
	t.Parallel()

	var count int
	debounced := NewLeadingFuncDebounce(100*time.Millisecond, func() {
		time.Sleep(1 * time.Second)
		count++
	})

	debounced.Call()

	time.Sleep(50 * time.Millisecond)

	debounced.Call()

	time.Sleep(3 * time.Second)

	if count != 2 {
		t.Fatal("expected count to be 2")
	}
}
