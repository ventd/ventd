package envelope

import (
	"math"
	"time"
)

// thermalAbort returns true when the dT/dt rate exceeds the class threshold.
// For NAS (DTDtAbortCPerSec==0, DTDtAbortCPerMin>0) the per-minute rate is used.
// prev may be nil on the first sample; in that case no abort is triggered.
func thermalAbort(temps, prev map[string]float64, dt time.Duration, thr Thresholds) bool {
	if len(prev) == 0 || dt <= 0 {
		return false
	}
	if thr.DTDtAbortCPerSec == 0 && thr.DTDtAbortCPerMin == 0 {
		return false
	}
	for id, cur := range temps {
		p, ok := prev[id]
		if !ok {
			continue
		}
		delta := cur - p
		if thr.DTDtAbortCPerSec > 0 {
			rate := delta / dt.Seconds()
			if rate > thr.DTDtAbortCPerSec {
				return true
			}
		} else {
			rate := delta / dt.Minutes()
			if rate > thr.DTDtAbortCPerMin {
				return true
			}
		}
	}
	return false
}

// absoluteTempAbort returns true when any sensor reading exceeds the ceiling.
// For classes with TAbsOffsetBelowTjmax > 0 the ceiling is tjmax - offset.
// For NAS (TAbsAbsolute > 0) the ceiling is TAbsAbsolute.
func absoluteTempAbort(temps map[string]float64, tjmax float64, thr Thresholds) bool {
	var ceiling float64
	if thr.TAbsOffsetBelowTjmax > 0 {
		ceiling = tjmax - thr.TAbsOffsetBelowTjmax
	} else {
		ceiling = thr.TAbsAbsolute
	}
	if ceiling <= 0 {
		return false
	}
	for _, t := range temps {
		if t > ceiling {
			return true
		}
	}
	return false
}

// ambientHeadroomOK returns true when there is sufficient thermal headroom
// between ambient and Tjmax to safely run Envelope C.
func ambientHeadroomOK(ambient, tjmax float64, thr Thresholds) bool {
	return ambient < tjmax-thr.AmbientHeadroomMin
}

// copyTemps returns a shallow copy of a temperature map.
func copyTemps(src map[string]float64) map[string]float64 {
	if src == nil {
		return nil
	}
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// maxTemp returns the maximum value in the map or math.NaN() if empty.
func maxTemp(temps map[string]float64) float64 {
	max := math.NaN()
	for _, v := range temps {
		if math.IsNaN(max) || v > max {
			max = v
		}
	}
	return max
}
