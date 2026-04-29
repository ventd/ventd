package envelope

import "math"

// NASAbortTemp computes the per-drive T_abort using the NAS derate formula:
//
//	T_abort = min(50.0, mfgMax - 10.0, ambient + 15.0)
//
// All three bounds must be respected to protect both the drive and the user expectation.
func NASAbortTemp(mfgMax, ambient float64) float64 {
	return math.Min(50.0, math.Min(mfgMax-10.0, ambient+15.0))
}

// NASPoolAbortTemp returns the minimum per-drive T_abort across all drives in a pool.
// drives is a slice of (mfgMax, ambient) pairs.
func NASPoolAbortTemp(drives [][2]float64, ambient float64) float64 {
	if len(drives) == 0 {
		return 50.0
	}
	min := math.MaxFloat64
	for _, d := range drives {
		t := NASAbortTemp(d[0], ambient)
		if t < min {
			min = t
		}
	}
	return min
}
