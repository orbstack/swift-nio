package main

import (
	"errors"
	"net"
	"time"

	"k8s.io/klog/v2"
)

const (
	targetDuration = 10 * time.Second
	pingBufferSize = 64
	bulkBufferSize = 1024 * 1024
	pingIters      = 1000
)

func benchmarkVsock(conn net.Conn) error {
	defer conn.Close()
	klog.V(1).Info("benchmarking vsock")

	i := 0

	pingBuf := make([]byte, pingBufferSize)
	for i := 0; i < pingBufferSize; i++ {
		pingBuf[i] = 0xef
	}
	pingStart := time.Now()
	for i := 0; i < pingIters; i++ {
		count, err := conn.Write(pingBuf)
		if err != nil {
			return err
		}
		if count != pingBufferSize {
			return errors.New("short write")
		}

		count, err = conn.Read(pingBuf)
		if err != nil {
			return err
		}
		if count != pingBufferSize {
			return errors.New("short read")
		}
	}
	pingDuration := time.Since(pingStart)
	rtt := pingDuration / pingIters
	klog.V(1).Info("ping rtt: %v ms", rtt.Seconds()*1000)

	bulkBuf := make([]byte, bulkBufferSize)
	for i := 0; i < bulkBufferSize; i++ {
		bulkBuf[i] = 0xad
	}
	bulkStart := time.Now()
	for {
		count, err := conn.Write(bulkBuf)
		if err != nil {
			return err
		}
		if count != bulkBufferSize {
			return errors.New("short write")
		}

		i++
		if i%100 == 0 && time.Since(bulkStart) > targetDuration {
			break
		}
	}
	bulkDuration := time.Since(bulkStart)
	// bytes per second
	bandwidth := float64(bulkBufferSize*i) / bulkDuration.Seconds()
	gbps := bandwidth * 8 / 1000 / 1000 / 1000
	klog.V(1).Info("bulk speed host->guest: %v Gbps", gbps)

	// flip direction
	time.Sleep(250 * time.Millisecond)
	flipBuf := make([]byte, 1)
	flipBuf[0] = 0x42
	count, err := conn.Write(flipBuf)
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("short write")
	}

	// wait for signal
	count, err = conn.Read(flipBuf)
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("short read")
	}
	if flipBuf[0] != 0x42 {
		return errors.New("bad flip")
	}

	bulkStart = time.Now()
	for {
		count, err := conn.Read(bulkBuf)
		if err != nil {
			return err
		}
		if count != bulkBufferSize {
			return errors.New("short read")
		}

		i++
		if i%100 == 0 && time.Since(bulkStart) > targetDuration {
			break
		}
	}
	bulkDuration = time.Since(bulkStart)
	// bytes per second
	bandwidth = float64(bulkBufferSize*i) / bulkDuration.Seconds()
	gbps = bandwidth * 8 / 1000 / 1000 / 1000
	klog.V(1).Info("bulk speed guest->host: %v Gbps", gbps)

	return nil
}
