package osver

import "testing"

func TestOsVersionAtLeast(t *testing.T) {
	t.Parallel()

	if !IsAtLeast("v12.0") {
		t.Fatal("expected macOS 12.0 or later")
	}
}
