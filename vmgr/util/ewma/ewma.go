package ewma

import (
	"math/bits"
)

type EwmaF32 struct {
	Value  float32
	weight float32
}

func NewF32(initial float32, weight float32) EwmaF32 {
	return EwmaF32{
		Value:  initial,
		weight: weight,
	}
}

func (e *EwmaF32) Update(sample float32) float32 {
	e.Value = e.Value*e.weight + sample*(1-e.weight)
	return e.Value
}

func CeilILog2(x uint) int {
	if x <= 1 {
		return 0
	}
	return bits.Len(x - 1)
}

func FloorILog2(x uint) int {
	if x <= 1 {
		return 0
	}
	return bits.Len(x) - 1
}
