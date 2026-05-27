package config

import (
	"reflect"
	"testing"
)

// TestClone_NilSafe is the chain-without-guard contract.
func TestClone_NilSafe(t *testing.T) {
	var c *Config
	if got := c.Clone(); got != nil {
		t.Errorf("Clone(nil) = %v, want nil", got)
	}
}

// TestClone_EqualToOriginal pins the "deep copy is value-equal" half
// of the contract: reflect.DeepEqual must report no diff between the
// source and the clone.
func TestClone_EqualToOriginal(t *testing.T) {
	one := uint8(1)
	fiveZ := uint8(50)
	src := &Config{
		Version: 7,
		Sensors: []Sensor{{Name: "cpu", Type: "hwmon", Path: "/sys/.../temp1_input"}},
		Fans:    []Fan{{Name: "front", Type: "hwmon", PWMPath: "/sys/.../pwm1", MinPWM: 40, MaxPWM: 255}},
		Curves: []CurveConfig{{
			Name:    "default",
			Type:    "linear",
			Sensor:  "cpu",
			MinTemp: 40, MaxTemp: 80,
			MinPWM: 40, MaxPWM: 255,
			Points:    []CurvePoint{{Temp: 40, PWM: 40}, {Temp: 80, PWM: 255}},
			MinPWMPct: &fiveZ,
		}},
		Controls: []Control{{Fan: "front", Curve: "default", ManualPWM: &one}},
		Profiles: map[string]Profile{
			"quiet": {Bindings: map[string]string{"front": "quiet-curve"}},
		},
	}
	clone := src.Clone()
	if !reflect.DeepEqual(src, clone) {
		t.Fatalf("Clone is not DeepEqual to source:\n  src=%+v\nclone=%+v", src, clone)
	}
}

// TestClone_FullyDeepCopiesSlicesAndMaps pins the "no aliasing" half
// of the contract: mutating any slice/map in the clone MUST NOT
// touch the source. This is the actual issue #978 regression
// guard — the pre-Clone shallow copy silently aliased everything but
// the explicitly re-made fields.
func TestClone_FullyDeepCopiesSlicesAndMaps(t *testing.T) {
	one := uint8(1)
	src := &Config{
		Sensors:  []Sensor{{Name: "cpu"}},
		Fans:     []Fan{{Name: "front"}},
		Curves:   []CurveConfig{{Name: "default", Sources: []string{"a"}, Points: []CurvePoint{{Temp: 40, PWM: 40}}}},
		Controls: []Control{{Fan: "front", Curve: "default", ManualPWM: &one}},
		Profiles: map[string]Profile{"quiet": {Bindings: map[string]string{"front": "q"}}},
	}
	clone := src.Clone()

	// Mutate every container in the clone; the source must stay put.
	clone.Sensors[0].Name = "MUTATED"
	clone.Fans[0].Name = "MUTATED"
	clone.Curves[0].Sources[0] = "MUTATED"
	clone.Curves[0].Points[0].Temp = 999
	clone.Controls[0].Fan = "MUTATED"
	*clone.Controls[0].ManualPWM = 99
	clone.Profiles["quiet"].Bindings["front"] = "MUTATED"

	if src.Sensors[0].Name != "cpu" {
		t.Errorf("Sensors aliased: src=%q", src.Sensors[0].Name)
	}
	if src.Fans[0].Name != "front" {
		t.Errorf("Fans aliased: src=%q", src.Fans[0].Name)
	}
	if src.Curves[0].Sources[0] != "a" {
		t.Errorf("Curves.Sources aliased: src=%q", src.Curves[0].Sources[0])
	}
	if src.Curves[0].Points[0].Temp != 40 {
		t.Errorf("Curves.Points aliased: src=%v", src.Curves[0].Points[0].Temp)
	}
	if src.Controls[0].Fan != "front" {
		t.Errorf("Controls aliased: src=%q", src.Controls[0].Fan)
	}
	if *src.Controls[0].ManualPWM != 1 {
		t.Errorf("Controls.ManualPWM ptr aliased: src=%d", *src.Controls[0].ManualPWM)
	}
	if src.Profiles["quiet"].Bindings["front"] != "q" {
		t.Errorf("Profiles.Bindings aliased: src=%q", src.Profiles["quiet"].Bindings["front"])
	}
}

// TestClone_PIPointerFieldsUnaliased pins the PI-curve specific
// *float64 / *uint8 fields — these were the second wave of pointer
// aliasing risk: validate() populates them as pointers so the
// runtime can distinguish "not set" from "explicit zero", and a
// shallow clone would leave both pointers sharing one float.
func TestClone_PIPointerFieldsUnaliased(t *testing.T) {
	sp := float64(60)
	kp := float64(1.5)
	ki := float64(0.05)
	ic := float64(100)
	ff := uint8(40)
	src := &Config{
		Curves: []CurveConfig{{
			Name: "pi", Type: "pi",
			Setpoint: &sp, Kp: &kp, Ki: &ki,
			IntegralClamp: &ic, FeedForward: &ff,
		}},
	}
	clone := src.Clone()
	*clone.Curves[0].Setpoint = 999
	*clone.Curves[0].Kp = 999
	*clone.Curves[0].Ki = 999
	*clone.Curves[0].IntegralClamp = 999
	*clone.Curves[0].FeedForward = 99
	if *src.Curves[0].Setpoint != 60 {
		t.Errorf("Setpoint aliased: src=%v", *src.Curves[0].Setpoint)
	}
	if *src.Curves[0].Kp != 1.5 {
		t.Errorf("Kp aliased: src=%v", *src.Curves[0].Kp)
	}
	if *src.Curves[0].Ki != 0.05 {
		t.Errorf("Ki aliased: src=%v", *src.Curves[0].Ki)
	}
	if *src.Curves[0].IntegralClamp != 100 {
		t.Errorf("IntegralClamp aliased: src=%v", *src.Curves[0].IntegralClamp)
	}
	if *src.Curves[0].FeedForward != 40 {
		t.Errorf("FeedForward aliased: src=%v", *src.Curves[0].FeedForward)
	}
}

// TestClone_AcousticOptimisationPointerUnaliased pins the top-level
// *bool toggle — same aliasing class as the PI fields.
func TestClone_AcousticOptimisationPointerUnaliased(t *testing.T) {
	t1 := true
	src := &Config{AcousticOptimisation: &t1}
	clone := src.Clone()
	*clone.AcousticOptimisation = false
	if *src.AcousticOptimisation != true {
		t.Errorf("AcousticOptimisation ptr aliased: src=%v", *src.AcousticOptimisation)
	}
}

// TestClone_SmartDisabledPointerUnaliased verifies Clone deep-copies the
// Smart.Disabled pointer-bool so a clone can be mutated without writing
// through to the live config (mirrors the AcousticOptimisation case).
func TestClone_SmartDisabledPointerUnaliased(t *testing.T) {
	tr := true
	src := &Config{Smart: SmartConfig{Disabled: &tr}}
	clone := src.Clone()
	*clone.Smart.Disabled = false
	if *src.Smart.Disabled != true {
		t.Errorf("Smart.Disabled ptr aliased: src=%v", *src.Smart.Disabled)
	}
}

// TestClone_EmptyContainersStayEmpty pins the "nil-vs-empty" round-trip:
// a nil slice/map in the source must stay nil in the clone (not get
// promoted to a non-nil empty), so reflect.DeepEqual round-trips and
// YAML round-trips stay stable.
func TestClone_EmptyContainersStayEmpty(t *testing.T) {
	src := &Config{Version: 1}
	clone := src.Clone()
	if clone.Sensors != nil {
		t.Errorf("nil Sensors became %v after Clone", clone.Sensors)
	}
	if clone.Fans != nil {
		t.Errorf("nil Fans became %v after Clone", clone.Fans)
	}
	if clone.Curves != nil {
		t.Errorf("nil Curves became %v after Clone", clone.Curves)
	}
	if clone.Controls != nil {
		t.Errorf("nil Controls became %v after Clone", clone.Controls)
	}
	if clone.Profiles != nil {
		t.Errorf("nil Profiles became %v after Clone", clone.Profiles)
	}
}
