package agent

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	maxFds = 16
	// every 10 min, GC if not used in 5 min
	fdxGcInterval  = 10 * time.Minute
	fdxGcThreshold = 5 * time.Minute
)

type pendingFdx struct {
	fds []int
	err error
	ch  chan struct{}
}

type queuedFdx struct {
	time time.Time
	fds  []int
}

type Fdx struct {
	conn          *net.UnixConn
	seq           atomic.Uint64
	pendingReads  map[uint64]*pendingFdx
	pendingQueued map[uint64]queuedFdx
	mu            sync.Mutex
	err           error
	stopChan      chan struct{}
}

func NewFdx(conn net.Conn) *Fdx {
	fdx := &Fdx{
		conn:          conn.(*net.UnixConn),
		pendingReads:  make(map[uint64]*pendingFdx),
		pendingQueued: make(map[uint64]queuedFdx),
		stopChan:      make(chan struct{}),
	}

	go func() {
		err := fdx.readLoop()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			logrus.WithError(err).Error("fdx read loop failed")
		}
	}()

	go func() {
		err := fdx.gcLoop()
		if err != nil {
			logrus.WithError(err).Error("fdx gc loop failed")
		}
	}()

	return fdx
}

func (f *Fdx) closeWithErr(err error) error {
	// close pending
	f.mu.Lock()
	defer f.mu.Unlock()

	// stopChan is unbuffered and will hang if already stopped
	if f.err != nil {
		return nil
	}

	for seq, pending := range f.pendingReads {
		// should be received
		if pending.fds != nil {
			continue
		}

		pending.err = err
		pending.ch <- struct{}{}
		close(pending.ch)
		delete(f.pendingReads, seq)
	}
	for seq, queued := range f.pendingQueued {
		closeAll(queued.fds)
		delete(f.pendingQueued, seq)
	}
	f.err = err
	close(f.stopChan) // broadcast

	return f.conn.Close()
}

func (f *Fdx) Close() error {
	return f.closeWithErr(net.ErrClosed)
}

func (f *Fdx) nextSeq() uint64 {
	return f.seq.Add(1)
}

func closeAll(fds []int) {
	for _, fd := range fds {
		unix.Close(fd)
	}
}

func (f *Fdx) gcLoop() error {
	ticker := time.NewTicker(fdxGcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			f.mu.Lock()
			for seq, queued := range f.pendingQueued {
				if time.Since(queued.time) > fdxGcThreshold {
					closeAll(queued.fds)
					delete(f.pendingQueued, seq)
				}
			}
			f.mu.Unlock()
		case <-f.stopChan:
			return nil
		}
	}
}

func (f *Fdx) readLoop() (err error) {
	minOob := unix.CmsgSpace(4)
	oob := make([]byte, unix.CmsgSpace(4*maxFds))
	msg := make([]byte, 8)

	defer func() {
		if err != nil {
			f.closeWithErr(err)
		} else {
			f.Close()
		}
	}()

	for {
		// use f.conn.ReadMsgUnix
		var n int
		var oobn int
		n, oobn, _, _, err = f.conn.ReadMsgUnix(msg, oob)
		if err != nil {
			return
		}
		if oobn < minOob {
			err = errors.New("short oob read")
			return
		}
		if n != len(msg) {
			err = errors.New("short msg read")
			return
		}

		var scms []unix.SocketControlMessage
		scms, err = unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return
		}
		if len(scms) != 1 {
			err = errors.New("unexpected number of socket control messages")
			return
		}

		// cloexec safe: Go sets MSG_CMSG_CLOEXEC
		var fds []int
		fds, err = unix.ParseUnixRights(&scms[0])
		if err != nil {
			return
		}

		seq := binary.LittleEndian.Uint64(msg)
		f.mu.Lock()
		pending, ok := f.pendingReads[seq]
		if ok {
			pending.fds = fds
			pending.err = nil
			pending.ch <- struct{}{}
			close(pending.ch)
			delete(f.pendingReads, seq)
		} else {
			f.pendingQueued[seq] = queuedFdx{
				time: time.Now(),
				fds:  fds,
			}
		}
		f.mu.Unlock()
	}
}

func (f *Fdx) SendFdsInt(fds ...int) (uint64, error) {
	if len(fds) > maxFds {
		return 0, errors.New("too many fds")
	}

	seq := f.nextSeq()
	msg := binary.LittleEndian.AppendUint64(nil, seq)

	oob := unix.UnixRights(fds...)
	n, oobn, err := f.conn.WriteMsgUnix(msg, oob, nil)
	if err != nil {
		return 0, err
	}
	if oobn != len(oob) {
		return 0, errors.New("short oob write")
	}
	if n != len(msg) {
		return 0, errors.New("short msg write")
	}
	return seq, nil
}

func (f *Fdx) RecvFdsInt(seq uint64) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if queued, ok := f.pendingQueued[seq]; ok {
		delete(f.pendingQueued, seq)
		return queued.fds, nil
	}
	if f.err != nil {
		return nil, f.err
	}
	pending := pendingFdx{
		ch: make(chan struct{}),
	}
	f.pendingReads[seq] = &pending

	f.mu.Unlock()
	<-pending.ch
	f.mu.Lock()
	if pending.err != nil {
		return nil, pending.err
	}
	return pending.fds, nil
}

func (f *Fdx) SendFdInt(fd int) (uint64, error) {
	return f.SendFdsInt(fd)
}

func (f *Fdx) RecvFdInt(seq uint64) (int, error) {
	fds, err := f.RecvFdsInt(seq)
	if err != nil {
		return 0, err
	}
	if len(fds) != 1 {
		return 0, errors.New("unexpected number of fds")
	}
	return fds[0], nil
}

func (f *Fdx) SendFiles(files ...*os.File) (uint64, error) {
	fds := make([]int, len(files))
	for i, file := range files {
		fds[i] = int(util.GetFd(file))
	}
	return f.SendFdsInt(fds...)
}

func (f *Fdx) SendFile(file *os.File) (uint64, error) {
	return f.SendFdInt(int(util.GetFd(file)))
}

func (f *Fdx) RecvFile(seq uint64) (*os.File, error) {
	fd, err := f.RecvFdInt(seq)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "fdx"), nil
}

func (f *Fdx) RecvFiles(seq uint64) ([]*os.File, error) {
	fds, err := f.RecvFdsInt(seq)
	if err != nil {
		return nil, err
	}
	files := make([]*os.File, len(fds))
	for i, fd := range fds {
		files[i] = os.NewFile(uintptr(fd), "fdx fd")
	}
	return files, nil
}
