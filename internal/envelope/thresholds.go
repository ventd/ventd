package envelope

import (
	"time"

	"github.com/ventd/ventd/internal/sysclass"
)

// Thresholds encodes per-class probe parameters for Envelope C/D.
type Thresholds struct {
	// DTDtAbortCPerSec is the per-second temperature rise rate that triggers abort.
	// Zero means use DTDtAbortCPerMin over DTDtWindow instead (NAS class).
	DTDtAbortCPerSec float64
	// DTDtAbortCPerMin is the per-minute rate; used only when DTDtAbortCPerSec == 0.
	DTDtAbortCPerMin float64
	// DTDtWindow is the sampling window for per-minute rate calculations.
	DTDtWindow time.Duration
	// TAbsOffsetBelowTjmax: abort when any sensor exceeds Tjmax - TAbsOffsetBelowTjmax.
	// Zero means use TAbsAbsolute instead.
	TAbsOffsetBelowTjmax float64
	// TAbsAbsolute is the fixed abort ceiling when TAbsOffsetBelowTjmax == 0.
	TAbsAbsolute float64
	// AmbientHeadroomMin: require ambient < Tjmax - AmbientHeadroomMin before starting.
	AmbientHeadroomMin float64
	// PWMSteps is the ordered list of PWM values to probe (Envelope C: descending, Envelope D: ascending above baseline).
	PWMSteps []uint8
	// Hold is the settle time at each step before sampling.
	Hold time.Duration
	// SampleHz is the sensor read rate during the hold window.
	SampleHz int
	// BMCGated: Envelope C requires --allow-server-probe when BMC is present.
	BMCGated bool
	// ECHandshakeRequired: must confirm EC responsiveness before probing.
	ECHandshakeRequired bool
}

// classThresholds maps each SystemClass to its probe parameters.
// ClassUnknown falls through to ClassMidDesktop (safe consumer default).
var classThresholds = map[sysclass.SystemClass]Thresholds{
	sysclass.ClassHEDTAir: {
		DTDtAbortCPerSec:     2.0,
		TAbsOffsetBelowTjmax: 15.0,
		AmbientHeadroomMin:   60.0,
		PWMSteps:             []uint8{180, 140, 110, 90, 70, 55, 40},
		Hold:                 30 * time.Second,
		SampleHz:             10,
	},
	sysclass.ClassHEDTAIO: {
		DTDtAbortCPerSec:     1.5,
		TAbsOffsetBelowTjmax: 15.0,
		AmbientHeadroomMin:   60.0,
		PWMSteps:             []uint8{180, 140, 110, 90, 70, 55, 40},
		Hold:                 45 * time.Second,
		SampleHz:             10,
	},
	sysclass.ClassMidDesktop: {
		DTDtAbortCPerSec:     1.5,
		TAbsOffsetBelowTjmax: 12.0,
		AmbientHeadroomMin:   55.0,
		PWMSteps:             []uint8{180, 140, 110, 90, 70, 55, 40},
		Hold:                 30 * time.Second,
		SampleHz:             10,
	},
	sysclass.ClassServer: {
		DTDtAbortCPerSec:     1.0,
		TAbsOffsetBelowTjmax: 20.0,
		AmbientHeadroomMin:   50.0,
		PWMSteps:             []uint8{200, 170, 140, 120, 100},
		Hold:                 30 * time.Second,
		SampleHz:             10,
		BMCGated:             true,
	},
	sysclass.ClassLaptop: {
		DTDtAbortCPerSec:     2.0,
		TAbsOffsetBelowTjmax: 15.0,
		AmbientHeadroomMin:   55.0,
		PWMSteps:             []uint8{180, 140, 110, 90, 70, 55, 40},
		Hold:                 30 * time.Second,
		SampleHz:             10,
		ECHandshakeRequired:  true,
	},
	sysclass.ClassMiniPC: {
		DTDtAbortCPerSec:     1.0,
		TAbsOffsetBelowTjmax: 20.0,
		AmbientHeadroomMin:   55.0,
		PWMSteps:             []uint8{180, 140, 110, 90, 70},
		Hold:                 30 * time.Second,
		SampleHz:             10,
	},
	sysclass.ClassNASHDD: {
		DTDtAbortCPerMin: 1.0,
		DTDtWindow:       5 * time.Minute,
		TAbsAbsolute:     50.0,
		PWMSteps:         []uint8{200, 170, 140, 120, 100},
		Hold:             5 * time.Minute,
		SampleHz:         1,
	},
}

// LookupThresholds returns probe thresholds for the given class.
// ClassUnknown and unrecognised values return ClassMidDesktop thresholds.
func LookupThresholds(cls sysclass.SystemClass) Thresholds {
	if t, ok := classThresholds[cls]; ok {
		return t
	}
	return classThresholds[sysclass.ClassMidDesktop]
}
