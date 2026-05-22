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

// TestLookupSoftIdleThresholds_HomelabClassesAreLooserThanLaptop is a
// guardrail for the homelab-fitness fix: classes whose normal "idle"
// includes steady-state services (server, NAS, mini-PC) MUST tolerate
// more CPU PSI than the laptop class. The opposite ordering would
// reintroduce the 10 % global-ceiling bug that refused every probe on
// any 24/7 services box.
func TestLookupSoftIdleThresholds_HomelabClassesAreLooserThanLaptop(t *testing.T) {
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
		if thr.PSICpuAvg60 <= laptop.PSICpuAvg60 {
			t.Errorf("%v: PSICpuAvg60=%v must be > laptop %v "+
				"(homelab classes need looser thresholds)",
				cls, thr.PSICpuAvg60, laptop.PSICpuAvg60)
		}
	}
}
