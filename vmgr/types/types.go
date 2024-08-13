package types

type StopType int

const (
	StopTypeForce StopType = iota
	StopTypeGraceful
)

type StopReason int

const (
	// 100 + <unexpected stop reason, starting from 1>
	stopExitCodeBase = 100

	// normal reasons
	// Swift (MacVirt) and Rust (libkrun) use these constants, so use explicit values
	StopReasonSignal     StopReason = 0
	StopReasonAPI        StopReason = 1
	StopReasonUninstall  StopReason = 2
	StopReasonKillswitch StopReason = 3

	Start_UnexpectedStopReasons StopReason = 4

	// unexpected reasons
	StopReasonKernelPanic    StopReason = 5
	StopReasonDrm            StopReason = 6
	StopReasonHealthCheck    StopReason = 7
	StopReasonDataCorruption StopReason = 8
	StopReasonIOError        StopReason = 9
	StopReasonOutOfMemory    StopReason = 10
	StopReasonUnknownCrash   StopReason = 11
)

func (r StopReason) ExitCode() int {
	if r > Start_UnexpectedStopReasons {
		return stopExitCodeBase + int(r-Start_UnexpectedStopReasons)
	} else {
		return -1
	}
}

type StopRequest struct {
	Type   StopType
	Reason StopReason
}
