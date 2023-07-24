package main

import "testing"

// tests never run under rosetta
func TestVerifyRosetta(t *testing.T) {
	t.Parallel()

	err := verifyRosetta()
	if err != nil {
		t.Fatal(err)
	}
}
