package tcppump

import (
	"errors"
	"io"

	"github.com/orbstack/macvirt/vmgr/util/ewma"
)

var (
	ErrInvalidWrite = errors.New("invalid write result")
)

const (
	minBufferSize     = 16384
	maxBufferSize     = 2 * 1024 * 1024 // 2 MiB
	defaultBufferSize = 65536

	ZeroCopyGvBufferSize = 1 * 1024 * 1024 // 1 MiB

	ewmaWeight = 1.0 / 128.0
)

// io.CopyBuffer with ewma
// TODO: study generics, gcshape stenciling, dictionaries. does this get devirtualized? do we need monomorphized copies?
func CopyBuffer(dst io.Writer, src io.Reader, buf []byte) (written int64, err error) {
	// If the reader has a WriteTo method, use it to do the copy.
	// Avoids an allocation and a copy.
	if wt, ok := src.(io.WriterTo); ok {
		return wt.WriteTo(dst)
	}
	// Similarly, if the writer has a ReadFrom method, use it to do the copy.
	// MOD: but only if BOTH sides have it. (i.e. both are tcp or unix)
	if rt, ok := dst.(io.ReaderFrom); ok {
		if _, ok := src.(io.ReaderFrom); ok {
			return rt.ReadFrom(src)
		}
	}
	if buf == nil {
		// TODO use gvisor view pooling
		// this is also good to avoid allocating buffer if using ReadFrom
		buf = make([]byte, defaultBufferSize)
	}
	avg := ewma.NewF32(float32(len(buf)), ewmaWeight)
	bufThresHi := uint64(len(buf) * 3 / 4)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = ErrInvalidWrite
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}

			// only scale up, not down.
			// - prevents oscillating
			// - no need for ratelimit, so it scales up fast
			// TODO consider jumping by 2 powers of 2
			newAvg := uint64(avg.Update(float32(nr)))
			if newAvg > bufThresHi && len(buf) < maxBufferSize {
				// next pow2 - move up 2 powers of 2
				targetSize := nextPow2(nextPow2(len(buf)))
				targetSize = min(maxBufferSize, targetSize)
				targetSize = max(minBufferSize, targetSize)

				buf = make([]byte, targetSize)
				bufThresHi = uint64(len(buf) * 3 / 4)
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

func nextPow2(x int) int {
	return 1 << ewma.CeilILog2(uint(x+1))
}
