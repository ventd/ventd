package curve

import (
	"fmt"
	"math"
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

func (c *Mix) Evaluate(sensors map[string]float64) uint8 {
	if len(c.Sources) == 0 {
		return 0
	}
	vals := make([]uint8, len(c.Sources))
	for i, src := range c.Sources {
		vals[i] = src.Evaluate(sensors)
	}
	switch c.Function {
	case MixMax:
		m := vals[0]
		for _, v := range vals[1:] {
			if v > m {
				m = v
			}
		}
		return m
	case MixMin:
		m := vals[0]
		for _, v := range vals[1:] {
			if v < m {
				m = v
			}
		}
		return m
	case MixAverage:
		sum := 0
		for _, v := range vals {
			sum += int(v)
		}
		return uint8(math.Round(float64(sum) / float64(len(vals))))
	default:
		return vals[0]
	}
}
