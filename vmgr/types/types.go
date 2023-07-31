package types

type StopType int

const (
	StopTypeForce StopType = iota
	StopTypeGraceful
)

type StopReason int

const (
	// normal reasons
	StopReasonSignal StopReason = iota
	StopReasonAPI
	StopReasonUninstall
	StopReasonKillswitch

	Start_UnexpectedStopReasons

	// unexpected reasons
	StopReasonPanic
	StopReasonDrm
	StopReasonHealthCheck
)

type StopRequest struct {
	Type   StopType
	Reason StopReason
}
