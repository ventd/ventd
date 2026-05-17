package setup

import "testing"

// TestIsAllPhantom_NvidiaOnlyHostIsNotPhantom pins issue #1059 (F2): a host
// with only NVIDIA fans (headless GPU compute box, no exposed SuperIO chip)
// must not be classified as all-phantom. NVML fans have full control surface
// and no phantom failure mode; demoting to monitor-only here would silently
// disable working GPU fan control.
func TestIsAllPhantom_NvidiaOnlyHostIsNotPhantom(t *testing.T) {
	fans := []FanState{
		{Name: "GPU 0 fan", Type: "nvidia", PWMPath: "0"},
		{Name: "GPU 1 fan", Type: "nvidia", PWMPath: "1"},
	}
	if isAllPhantom(fans) {
		t.Fatalf("NVIDIA-only host classified as all-phantom; controller would be wrongly demoted to monitor-only (#1059)")
	}
}

// TestIsAllPhantom_MixedNvidiaAndPhantomHwmonIsNotPhantom: a host where
// every hwmon channel is phantom but at least one NVML fan exists must
// stay in active-control mode (the NVIDIA fan is independently controllable).
func TestIsAllPhantom_MixedNvidiaAndPhantomHwmonIsNotPhantom(t *testing.T) {
	fans := []FanState{
		{Name: "Phantom 1", Type: "hwmon", DetectPhase: "found", PolarityPhase: "phantom"},
		{Name: "GPU fan", Type: "nvidia", PWMPath: "0"},
	}
	if isAllPhantom(fans) {
		t.Fatalf("mixed nvidia+phantom-hwmon classified as all-phantom; demoting to monitor-only would disable working GPU control (#1059)")
	}
}

// TestIsAllPhantom_HeuristicHwmonCountsAsControllable: heuristic-bound hwmon
// fans responded to PWM but had no RPM correlate. Open-loop control is still
// real fan control; they must NOT trigger monitor-only demotion.
func TestIsAllPhantom_HeuristicHwmonCountsAsControllable(t *testing.T) {
	fans := []FanState{
		{Name: "Open-loop 1", Type: "hwmon", DetectPhase: "heuristic", PolarityPhase: "normal"},
		{Name: "Phantom 1", Type: "hwmon", DetectPhase: "found", PolarityPhase: "phantom"},
	}
	if isAllPhantom(fans) {
		t.Fatalf("heuristic-bound fan classified as phantom; open-loop control would be wrongly demoted")
	}
}

// TestIsAllPhantom_AllHwmonPhantomReturnsTrue: the actual monitor-only
// case — every hwmon channel firmware-locked and no NVIDIA fans on the box.
// This is the legitimate Dell PE 14G / HPE iLO5 path.
func TestIsAllPhantom_AllHwmonPhantomReturnsTrue(t *testing.T) {
	fans := []FanState{
		{Name: "Phantom 1", Type: "hwmon", DetectPhase: "found", PolarityPhase: "phantom"},
		{Name: "Phantom 2", Type: "hwmon", DetectPhase: "found", PolarityPhase: "phantom"},
	}
	if !isAllPhantom(fans) {
		t.Fatalf("all-firmware-locked hwmon host not classified as all-phantom; monitor-only demotion is the documented contract")
	}
}

// TestIsAllPhantom_EmptyFanListIsAllPhantom: no fans at all means nothing
// to control. The wizard reaches this via a different code path (the
// detect step's "no fan controllers were found" branch), but the helper
// must report true here for the boundary case to be unambiguous.
func TestIsAllPhantom_EmptyFanListIsAllPhantom(t *testing.T) {
	if !isAllPhantom(nil) {
		t.Fatalf("empty fan list should be classified as all-phantom (no controllable channel exists)")
	}
}

// TestIsAllPhantom_DetectNoneHwmonDoesNotCount: a fan that detection
// could not bind ("none" — neither found nor heuristic) is not
// controllable, so its presence must not flip the phantom verdict.
func TestIsAllPhantom_DetectNoneHwmonDoesNotCount(t *testing.T) {
	fans := []FanState{
		{Name: "Undetected", Type: "hwmon", DetectPhase: "none"},
	}
	if !isAllPhantom(fans) {
		t.Fatalf("undetected hwmon (DetectPhase=none) wrongly counted as controllable")
	}
}
