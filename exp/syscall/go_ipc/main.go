package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	NS  = 1
	US  = 1000 * NS
	MS  = 1000 * US
	SEC = 1000 * MS

	DURATION    = 10 * SEC
	BUCKET_SIZE = 3
)

func now() uint64 {
	return uint64(time.Now().UnixNano())
}

func main() {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	// receiver
	var wg sync.WaitGroup
	go func() {
		wg.Add(1)
		defer wg.Done()

		var buckets [65536]uint64
		var totalLat uint64
		var iters uint64

		for {
			var buf [8]byte
			n, err := r.Read(buf[:])
			if err != nil || n != 8 {
				fmt.Println("read error")
				break
			}
			recvTs := binary.LittleEndian.Uint64(buf[:])
			sendTs := now()
			latency := (sendTs - recvTs) / 1000
			totalLat += latency
			iters++
			bucket := latency / BUCKET_SIZE
			if bucket < 65536 {
				buckets[bucket]++
			} else {
				buckets[65535]++
			}
		}

		// print avg
		fmt.Printf("avg latency: %d\n", totalLat/iters)

		// print median
		sum := uint64(0)
		for i := 0; i < 65536; i++ {
			sum += buckets[i]
			if sum > iters/2 {
				fmt.Printf("median: %d\n", i*BUCKET_SIZE)
				break
			}
		}
		fmt.Printf("\n")

		// print buckets
		for i := 0; i < 65536; i++ {
			if buckets[i] > 1 {
				fmt.Printf("%d-%d: %d\n", i*BUCKET_SIZE, (i+1)*BUCKET_SIZE, buckets[i])
			}
		}
	}()

	// sender
	start := now()
	for {
		sendTs := now()
		if sendTs-start > DURATION {
			break
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], sendTs)
		n, err := w.Write(buf[:])
		if err != nil {
			panic(err)
		}
		if n != 8 {
			panic("write error")
		}
		time.Sleep(time.Millisecond)
	}

	// wait for receiver
	r.Close()
	w.Close()
	wg.Wait()
}
