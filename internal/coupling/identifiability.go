package coupling

import (
	"errors"
	"math"

	"gonum.org/v1/gonum/mat"
)

// Identifiability thresholds per R9 §9.4.
const (
	HealthyKappaThreshold        = 100.0   // κ ≤ 10²
	UnidentifiableKappaThreshold = 10000.0 // κ > 10⁴
)

// Window controls the windowed regressor for the κ detector
// per R10 §10.2: W=60 samples, subsampled at 1/10 of tick rate.
type Window struct {
	d        int
	capacity int
	rows     [][]float64 // ring buffer of φ rows
	head     int
	count    int

	// Rolling ΦᵀΦ kept up to date incrementally.
	mInv *mat.Dense
}

// NewWindow constructs a windowed regressor with capacity W rows
// of dimension d.
func NewWindow(d, capacity int) *Window {
	if capacity <= 0 {
		capacity = 60
	}
	return &Window{
		d:        d,
		capacity: capacity,
		rows:     make([][]float64, capacity),
		mInv:     mat.NewDense(d, d, nil),
	}
}

// Add appends one φ row to the window, evicting the oldest if
// full. Computes the rolling ΦᵀΦ incrementally.
func (w *Window) Add(phi []float64) error {
	if len(phi) != w.d {
		return errors.New("coupling: phi length != window dim")
	}
	row := make([]float64, w.d)
	copy(row, phi)
	w.rows[w.head] = row
	w.head = (w.head + 1) % w.capacity
	if w.count < w.capacity {
		w.count++
	}
	return nil
}

// Count returns the number of rows currently in the window.
func (w *Window) Count() int { return w.count }

// PhiTPhi returns the current ΦᵀΦ / W matrix where W is the
// effective window size (count, not capacity, until full).
// Returns a copy; safe to retain.
func (w *Window) PhiTPhi() *mat.SymDense {
	out := mat.NewSymDense(w.d, nil)
	if w.count == 0 {
		return out
	}
	for i := 0; i < w.count; i++ {
		row := w.rows[i]
		if row == nil {
			continue
		}
		for r := 0; r < w.d; r++ {
			for c := r; c < w.d; c++ {
				out.SetSym(r, c, out.At(r, c)+row[r]*row[c])
			}
		}
	}
	scale := 1.0 / float64(w.count)
	for r := 0; r < w.d; r++ {
		for c := r; c < w.d; c++ {
			out.SetSym(r, c, out.At(r, c)*scale)
		}
	}
	return out
}

// Kappa returns the 2-norm condition number of ΦᵀΦ / W via
// gonum's mat.Cond. Microseconds per call for d ≤ 26 per
// R10 §10.8.
func (w *Window) Kappa() float64 {
	if w.count < w.d {
		return math.Inf(1) // not enough samples to be conditioned
	}
	m := w.PhiTPhi()
	// Convert to *mat.Dense for Cond which expects mat.Matrix.
	// SymDense satisfies mat.Matrix, so this is direct.
	return mat.Cond(m, 2)
}

// ClassifyKappa returns the SnapshotKind appropriate for the
// given κ value per R9 §9.4 / RULE-CPL-IDENT-02.
func ClassifyKappa(kappa float64) SnapshotKind {
	switch {
	case math.IsInf(kappa, 0) || math.IsNaN(kappa):
		return KindUnidentifiable
	case kappa <= HealthyKappaThreshold:
		return KindHealthy
	case kappa <= UnidentifiableKappaThreshold:
		return KindMarginal
	default:
		return KindUnidentifiable
	}
}

// CoVaryingThreshold is the Pearson correlation above which two
// columns are declared co-varying per R10 §9.4.
//
// We use the SIGNED ρ > threshold (not |ρ|): R10 defines
// "co-varying" as "two fans always commanded the same PWM" —
// dual CPU fans on the same header, daisy-chained Y-cable. That's
// a positive-correlation relationship. Anti-correlated fans
// (one ramps up while the other ramps down) are a DIFFERENT
// physical relationship and should not be merged into a composite.
const CoVaryingThreshold = 0.999

// FindCoVaryingPairs returns the pairs of column indices in
// the window's ΦᵀΦ whose pairwise Pearson correlation exceeds
// CoVaryingThreshold. Empty slice when none found.
//
// Only fan-PWM columns (indices 1..NCoupled) are pairwise
// checked; the temperature-autoregressive column (index 0) and
// the load column (index NCoupled+1) are excluded.
func (w *Window) FindCoVaryingPairs(nCoupled int) [][2]int {
	if w.count < w.d || nCoupled < 2 {
		return nil
	}
	// Pearson over column pairs in the windowed regressor.
	// Compute means + variances + covariances over the count rows.
	means := make([]float64, w.d)
	for i := 0; i < w.count; i++ {
		row := w.rows[i]
		for c := 0; c < w.d; c++ {
			means[c] += row[c]
		}
	}
	for c := 0; c < w.d; c++ {
		means[c] /= float64(w.count)
	}

	variances := make([]float64, w.d)
	for i := 0; i < w.count; i++ {
		row := w.rows[i]
		for c := 0; c < w.d; c++ {
			d := row[c] - means[c]
			variances[c] += d * d
		}
	}

	var pairs [][2]int
	// Fan-PWM columns are at indices [1, 1+nCoupled).
	for j1 := 1; j1 < 1+nCoupled; j1++ {
		for j2 := j1 + 1; j2 < 1+nCoupled; j2++ {
			cov := 0.0
			for i := 0; i < w.count; i++ {
				row := w.rows[i]
				cov += (row[j1] - means[j1]) * (row[j2] - means[j2])
			}
			if variances[j1] == 0 || variances[j2] == 0 {
				continue
			}
			rho := cov / math.Sqrt(variances[j1]*variances[j2])
			if rho > CoVaryingThreshold {
				pairs = append(pairs, [2]int{j1, j2})
			}
		}
	}
	return pairs
}
