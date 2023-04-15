package istypes

type FsnotifyEventFlags uint32

const (
	FsnotifyEventCreate FsnotifyEventFlags = 1 << iota
	FsnotifyEventModify
	FsnotifyEventStatAttr
	FsnotifyEventRemove
)

type FsnotifyEventsBatch struct {
	Paths []string
	Flags []FsnotifyEventFlags
}
