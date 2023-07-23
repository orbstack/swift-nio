package util

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	errTest = errors.New("test error")
)

func TestTimeoutOk(t *testing.T) {
	t.Parallel()

	fn := func() (string, error) {
		return "ok", nil
	}
	result, err := WithTimeout(fn, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatal("result is not ok")
	}
}

func TestTimeoutExceeded(t *testing.T) {
	t.Parallel()

	fn := func() (string, error) {
		time.Sleep(2 * time.Second)
		return "ok", nil
	}
	_, err := WithTimeout(fn, 1*time.Second)
	if err != context.DeadlineExceeded {
		t.Fatal("expected DeadlineExceeded error")
	}
}

func TestTimeoutError(t *testing.T) {
	t.Parallel()

	fn := func() (string, error) {
		return "", errTest
	}
	_, err := WithTimeout(fn, 1*time.Second)
	if err != errTest {
		t.Fatal("expected test error")
	}
}

func TestTimeoutErrorAfterTimeout(t *testing.T) {
	t.Parallel()

	fn := func() (string, error) {
		time.Sleep(2 * time.Second)
		return "", errTest
	}
	_, err := WithTimeout(fn, 1*time.Second)
	if err != context.DeadlineExceeded {
		t.Fatal("expected DeadlineExceeded error")
	}
}
