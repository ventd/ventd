package idle

import (
	"testing"

	"github.com/ventd/ventd/internal/sysclass"
)

// TestLookupSoftIdleThresholds_AllKnownClasses asserts every defined
// SystemClass yields a populated threshold record with PSI CPU/IO
// > 0 and LoadAvgPerCPU > 0 — i.e. nothing silently uses the
// zero-value (which would refuse every probe trivially).
func TestLookupSoftIdleThresholds_AllKnownClasses(t *testing.T) {
	classes := []sysclass.SystemClass{
		sysclass.ClassLaptop,
		sysclass.ClassMidDesktop,
		sysclass.ClassHEDTAir,
		sysclass.ClassHEDTAIO,
		sysclass.ClassServer,
		sysclass.ClassMiniPC,
		sysclass.ClassNASHDD,
	}
	for _, cls := range classes {
		thr := LookupSoftIdleThresholds(cls)
		if thr.PSICpuAvg60 <= 0 {
			t.Errorf("%v: PSICpuAvg60 not populated: got %v", cls, thr.PSICpuAvg60)
		}
		if thr.PSIIoAvg60 <= 0 {
			t.Errorf("%v: PSIIoAvg60 not populated: got %v", cls, thr.PSIIoAvg60)
		}
		if thr.PSIMemAvg60 <= 0 {
			t.Errorf("%v: PSIMemAvg60 not populated: got %v", cls, thr.PSIMemAvg60)
		}
		if thr.LoadAvgPerCPU <= 0 {
			t.Errorf("%v: LoadAvgPerCPU not populated: got %v", cls, thr.LoadAvgPerCPU)
		}
	}
}

// TestLookupSoftIdleThresholds_UnknownFallsThroughToMidDesktop asserts
// the safe-default contract: ClassUnknown returns the same record
// as ClassMidDesktop, matching internal/envelope/thresholds.go's
// LookupThresholds fallback.
func TestLookupSoftIdleThresholds_UnknownFallsThroughToMidDesktop(t *testing.T) {
	unknown := LookupSoftIdleThresholds(sysclass.ClassUnknown)
	mid := LookupSoftIdleThresholds(sysclass.ClassMidDesktop)
	if unknown != mid {
		t.Errorf("ClassUnknown lookup must match ClassMidDesktop; got %+v, want %+v",
			unknown, mid)
	}
}

// TestLookupSoftIdleThresholds_HomelabClassesAreAtLeastAsLooseAsLaptop
// is a guardrail for the homelab-fitness calibration: classes whose
// normal "idle" includes steady-state services (server, NAS,
// mini-PC, HEDT) MUST NOT be tighter than the laptop class. The
// opposite ordering would reintroduce the global-ceiling bug that
// refused every probe on any 24/7 services box. Mid-desktop ties
// laptop by design (both are user-facing classes whose IRQ + SSH
// checks already cover the "user actively interacting" axis); the
// strictly-looser classes are asserted separately below.
func TestLookupSoftIdleThresholds_HomelabClassesAreAtLeastAsLooseAsLaptop(t *testing.T) {
	laptop := LookupSoftIdleThresholds(sysclass.ClassLaptop)
	for _, cls := range []sysclass.SystemClass{
		sysclass.ClassServer,
		sysclass.ClassNASHDD,
		sysclass.ClassMiniPC,
		sysclass.ClassHEDTAir,
		sysclass.ClassHEDTAIO,
		sysclass.ClassMidDesktop,
	} {
		thr := LookupSoftIdleThresholds(cls)
		if thr.PSICpuAvg60 < laptop.PSICpuAvg60 {
			t.Errorf("%v: PSICpuAvg60=%v must be >= laptop %v "+
				"(homelab classes can't be tighter than the user-laptop class)",
				cls, thr.PSICpuAvg60, laptop.PSICpuAvg60)
		}
	}
}

// TestLookupSoftIdleThresholds_PureServerClassesAreStrictlyLooserThanLaptop
// is the stronger guardrail: classes with no interactive user
// session (server, NAS) MUST be strictly looser than laptop. If
// laptop and server ever end up at the same number, opportunistic
// learning under steady-state homelab load stops happening. Keep
// this separate from the at-least-as-loose check so the failure
// message is specific.
func TestLookupSoftIdleThresholds_PureServerClassesAreStrictlyLooserThanLaptop(t *testing.T) {
	laptop := LookupSoftIdleThresholds(sysclass.ClassLaptop)
	for _, cls := range []sysclass.SystemClass{
		sysclass.ClassServer,
		sysclass.ClassNASHDD,
	} {
		thr := LookupSoftIdleThresholds(cls)
		if thr.PSICpuAvg60 <= laptop.PSICpuAvg60 {
			t.Errorf("%v: PSICpuAvg60=%v must be > laptop %v "+
				"(headless classes need strictly looser PSI to admit during steady-state services)",
				cls, thr.PSICpuAvg60, laptop.PSICpuAvg60)
		}
	}
}
