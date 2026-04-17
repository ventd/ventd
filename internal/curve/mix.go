package curve

import (
	"fmt"
	"math"
	"sync"
)

// MixFunc is the aggregation function applied across a Mix curve's sources.
type MixFunc int

const (
	MixMax     MixFunc = iota // highest value among all sources
	MixMin                    // lowest value among all sources
	MixAverage                // rounded mean of all sources
)

// ParseMixFunc parses a function name from config ("max", "min", "average").
func ParseMixFunc(s string) (MixFunc, error) {
	switch s {
	case "max":
		return MixMax, nil
	case "min":
		return MixMin, nil
	case "average":
		return MixAverage, nil
	default:
		return 0, fmt.Errorf("unknown mix function %q (want: max, min, average)", s)
	}
}

// Mix evaluates multiple source curves and aggregates their outputs.
// This is the primary tool for multi-component fan control: e.g. drive a fan
// at max(cpu_curve, gpu_curve) so it responds to whichever is hotter.
type Mix struct {
	Sources  []Curve
	Function MixFunc
}

// Opt-6: pool []uint8 slices used for per-source results so Mix.Evaluate
// never allocates on the hot path. The pool holds *[]uint8 pointers so
// append can grow the backing array and keep the pointer updated.
var mixValsPool = sync.Pool{
	New: func() any { v := make([]uint8, 0, 4); return &v },
}

func (c *Mix) Evaluate(sensors map[string]float64) uint8 {
	if len(c.Sources) == 0 {
		return 0
	}
	// Opt-6: borrow a slice from the pool; reset, fill, aggregate, then
	// return it before we return the result.
	vp := mixValsPool.Get().(*[]uint8)
	*vp = (*vp)[:0]
	for _, src := range c.Sources {
		*vp = append(*vp, src.Evaluate(sensors))
	}
	vals := *vp
	var result uint8
	switch c.Function {
	case MixMax:
		m := vals[0]
		for _, v := range vals[1:] {
			if v > m {
				m = v
			}
		}
		result = m
	case MixMin:
		m := vals[0]
		for _, v := range vals[1:] {
			if v < m {
				m = v
			}
		}
		result = m
	case MixAverage:
		sum := 0
		for _, v := range vals {
			sum += int(v)
		}
		result = uint8(math.Round(float64(sum) / float64(len(vals))))
	default:
		result = vals[0]
	}
	*vp = (*vp)[:0]
	mixValsPool.Put(vp)
	return result
}
