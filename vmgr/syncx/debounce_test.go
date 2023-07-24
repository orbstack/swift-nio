package syncx

import (
	"testing"
	"time"
)

func TestLeadingFuncDebounce(t *testing.T) {
	t.Parallel()

	var count int
	debounced := NewLeadingFuncDebounce(func() {
		count++
	}, 100*time.Millisecond)

	debounced.Trigger()
	debounced.Trigger()
	debounced.Trigger()

	time.Sleep(200 * time.Millisecond)

	if count != 2 {
		t.Fatal("expected count to be 2")
	}

	debounced.Trigger()
	time.Sleep(200 * time.Millisecond)

	if count != 3 {
		t.Fatal("expected count to be 3; got", count)
	}

	debounced.Trigger()
	debounced.Trigger()
	time.Sleep(50 * time.Millisecond)

	if count != 4 {
		t.Fatal("expected count to be 4")
	}
}
