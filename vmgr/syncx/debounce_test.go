package syncx

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFuncDebounce(t *testing.T) {
	t.Parallel()

	var count atomic.Int32
	f := NewFuncDebounce(100*time.Millisecond, func() {
		count.Add(1)
	})

	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(200 * time.Millisecond)

	if count.Load() != 1 {
		t.Fatal("expected 1")
	}
}

func TestFuncDebounceCancel(t *testing.T) {
	t.Parallel()

	var count atomic.Int32
	f := NewFuncDebounce(100*time.Millisecond, func() {
		count.Add(1)
	})

	f.Call()
	time.Sleep(200 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Cancel()
	time.Sleep(200 * time.Millisecond)

	if count.Load() != 1 {
		t.Fatal("expected 1")
	}
}

func TestDebounceStress(t *testing.T) {
	earlyReturnC := make(chan struct{})
	hangC := make(chan struct{})

	done := make(chan struct{})
	go func() {
		for range 5 {
			var wg sync.WaitGroup
			for range 100000 {
				wg.Add(1)
				go func() {
					defer wg.Done()

					result := callThenCancelAndWait(t)
					switch result {
					case didNotRun:
					case earlyReturn:
						earlyReturnC <- struct{}{}
						return
					case froze:
						hangC <- struct{}{}
						return
					case okay:
					}
				}()
			}

			wg.Wait()
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-earlyReturnC:
		t.Fatal("earlyReturn")
	case <-hangC:
		t.Fatal("hang")
	}
}

type debounceResult int

const (
	didNotRun debounceResult = iota
	earlyReturn
	froze
	okay
)

func callThenCancelAndWait(t *testing.T) debounceResult {
	t.Helper()

	var ran atomic.Bool
	startedC := make(chan struct{})
	f := NewFuncDebounce(10*time.Millisecond, func() {
		startedC <- struct{}{}
		time.Sleep(1000 * time.Millisecond)
		ran.Store(true)
	})

	done := make(chan struct{})
	f.Call()
	go func() {
		<-startedC
		f.CancelAndWait()
		done <- struct{}{}
	}()

	select {
	case <-done:
		if !ran.Load() {
			return earlyReturn
		}
		return okay

	case <-time.After(10 * time.Second):
		return froze
	}
}

func TestCancelAndWaitConcurrent(t *testing.T) {
	t.Parallel()

	ranC := make(chan time.Time)
	f := NewFuncDebounce(10*time.Millisecond, func() {
		time.Sleep(1000 * time.Millisecond)
		println("ran", time.Now().String())
		ranC <- time.Now()
	})

	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()

	var timeRan *time.Time
LOOP:
	for {
		select {
		case when := <-ranC:
			if timeRan != nil {
				if when.Sub(*timeRan).Round(time.Millisecond) < 999*time.Millisecond {
					t.Fatal("ran concurrently")
				}
			}
			timeRan = &when
		case <-time.After(5 * time.Second):
			break LOOP
		}
	}
}

func TestMultipleCallsAndCancel(t *testing.T) {
	t.Parallel()

	var ran atomic.Int32
	f := NewFuncDebounce(100*time.Millisecond, func() {
		time.Sleep(1000 * time.Millisecond)
		ran.Add(1)
	})

	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)
	f.Call()
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		f.CancelAndWait()
		done <- struct{}{}
	}()

	select {
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	case <-done:
	}

	if ran.Load() != 0 {
		t.Fatal("failed to cancel after multiple calls")
	}
}

func TestDebounceCallsWithPause(t *testing.T) {
	t.Parallel()

	var ran atomic.Int32
	f := NewFuncDebounce(10*time.Millisecond, func() {
		ran.Add(1)
	})

	f.Call()
	time.Sleep(50 * time.Millisecond)

	f.Call()
	time.Sleep(50 * time.Millisecond)

	f.Call()
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		f.CancelAndWait()
		done <- struct{}{}
	}()
	time.Sleep(50 * time.Millisecond)

	f.Call()
	time.Sleep(50 * time.Millisecond)

	f.Call()
	time.Sleep(50 * time.Millisecond)

	f.Call()
	time.Sleep(50 * time.Millisecond)

	if ran.Load() != 6 {
		t.Fatalf("ran %d times", ran.Load())
	}
}
