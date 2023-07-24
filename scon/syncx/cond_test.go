package syncx

import "testing"

func TestCondBoolSetGet(t *testing.T) {
	t.Parallel()

	cond := NewCondBool()
	if cond.Get() {
		t.Fatal("expected false")
	}
	cond.Set(true)
	if !cond.Get() {
		t.Fatal("expected true")
	}
}

func TestCondBoolWait(t *testing.T) {
	t.Parallel()

	cond := NewCondBool()
	if cond.Get() {
		t.Fatal("expected false")
	}
	go func() {
		cond.Set(true)
	}()
	cond.Wait()
	if !cond.Get() {
		t.Fatal("expected true")
	}
}

func TestCondBoolWaitAlreadyTrue(t *testing.T) {
	t.Parallel()

	cond := NewCondBool()
	if cond.Get() {
		t.Fatal("expected false")
	}
	cond.Set(true)
	cond.Wait()
	if !cond.Get() {
		t.Fatal("expected true")
	}
}

func TestCondBoolRace(t *testing.T) {
	t.Parallel()

	cond := NewCondBool()
	if cond.Get() {
		t.Fatal("expected false")
	}
	go func() {
		cond.Set(true)
	}()
	go func() {
		cond.Set(true)
	}()
	cond.Wait()
	if !cond.Get() {
		t.Fatal("expected true")
	}
}

func TestCondValueSetGet(t *testing.T) {
	t.Parallel()

	cond := NewCondValue[int](0, 0)
	if cond.Get() != 0 {
		t.Fatal("expected 0")
	}
	cond.Set(1)
	if cond.Get() != 1 {
		t.Fatal("expected 1")
	}
}

func TestCondValueWait(t *testing.T) {
	t.Parallel()

	cond := NewCondValue[int](0, 0)
	if cond.Get() != 0 {
		t.Fatal("expected 0")
	}
	go func() {
		cond.Set(1)
	}()
	cond.Wait()
	if cond.Get() != 1 {
		t.Fatal("expected 1")
	}
}

func TestCondValueWaitAlreadyTrue(t *testing.T) {
	t.Parallel()

	cond := NewCondValue[int](0, 0)
	if cond.Get() != 0 {
		t.Fatal("expected 0")
	}
	cond.Set(1)
	cond.Wait()
	if cond.Get() != 1 {
		t.Fatal("expected 1")
	}
}

func TestCondValueRace(t *testing.T) {
	t.Parallel()

	cond := NewCondValue[int](0, 0)
	if cond.Get() != 0 {
		t.Fatal("expected 0")
	}
	go func() {
		cond.Set(1)
	}()
	go func() {
		cond.Set(1)
	}()
	cond.Wait()
	if cond.Get() != 1 {
		t.Fatal("expected 1")
	}
}
