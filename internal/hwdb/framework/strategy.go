package framework

// SpeedAt returns the fan duty percentage the strategy commands at temperature
// tempC, interpolating linearly between adjacent speedCurve anchors (the same
// model fw-fanctrl uses). Below the first anchor it holds the first speed;
// above the last it holds the last. A strategy with an empty curve returns 0.
//
// This lets a consumer (the doctor surface, a future wizard preset import)
// preview or adopt a vendored Framework curve without re-implementing the
// interpolation. It is a pure read over the vendored data.
func (s Strategy) SpeedAt(tempC int) int {
	pts := s.SpeedCurve
	if len(pts) == 0 {
		return 0
	}
	if tempC <= pts[0].TempC {
		return pts[0].SpeedPct
	}
	last := pts[len(pts)-1]
	if tempC >= last.TempC {
		return last.SpeedPct
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if tempC <= b.TempC {
			span := b.TempC - a.TempC
			if span <= 0 {
				return b.SpeedPct
			}
			return a.SpeedPct + (b.SpeedPct-a.SpeedPct)*(tempC-a.TempC)/span
		}
	}
	return last.SpeedPct
}
