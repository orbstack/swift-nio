package main

import (
	"fmt"
	"time"
)

const testWindow = 3 * time.Second

var testTimes = []time.Duration{
	1 * time.Nanosecond,
	250 * time.Nanosecond,
	1 * time.Microsecond,
	250 * time.Microsecond,
	500 * time.Microsecond,
	1 * time.Millisecond,
	2 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	37 * time.Millisecond,
	250 * time.Millisecond,
	1 * time.Second,
}

func testOne(sleepDuration time.Duration, timeLimit time.Duration) time.Duration {
	start := time.Now()
	sleepCount := 0
	for {
		time.Sleep(sleepDuration)
		sleepCount++
		if time.Since(start) > timeLimit {
			break
		}
	}
	end := time.Now()

	// calculate average delta
	expectedTotalSleep := sleepDuration * time.Duration(sleepCount)
	actualTotalSleep := end.Sub(start)
	delta := actualTotalSleep - expectedTotalSleep
	averageDelta := delta / time.Duration(sleepCount)

	return averageDelta
}

func main() {
	for _, sleepDuration := range testTimes {
		averageDelta := testOne(sleepDuration, testWindow)
		fmt.Println("sleep duration:", sleepDuration, "average delta:", averageDelta)
	}
}
