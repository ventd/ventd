// Package fixtures provides synthetic test helpers for the polarity probe tests.
package fixtures

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ventd/ventd/internal/polarity"
)

// FakeHwmon simulates hwmon sysfs file reads/writes for polarity probe tests.
// RPMSequence is consumed in order on each tach read.
type FakeHwmon struct {
	mu           sync.Mutex
	pwm          int
	rpmSeq       []int // values returned on successive readFile(tachPath) calls
	rpmIdx       int
	writes       []int // captured PWM writes
	writeFail    bool  // if true, writeFile returns an error
	tachReadFail bool  // if true, readFile(tachPath) returns an error
}

// NewFakeHwmon constructs a FakeHwmon with initial PWM and an RPM sequence.
func NewFakeHwmon(initialPWM int, rpmSeq []int) *FakeHwmon {
	return &FakeHwmon{pwm: initialPWM, rpmSeq: rpmSeq}
}

// SetWriteFail causes all subsequent WriteFile calls to return an error.
func (f *FakeHwmon) SetWriteFail(v bool) { f.writeFail = v }

// SetTachReadFail causes all subsequent tach reads to fail.
func (f *FakeHwmon) SetTachReadFail(v bool) { f.tachReadFail = v }

// ReadFile simulates os.ReadFile on sysfs paths.
func (f *FakeHwmon) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(path, "fan") {
		if f.tachReadFail {
			return nil, fmt.Errorf("fake: tach read fail")
		}
		var rpm int
		if f.rpmIdx < len(f.rpmSeq) {
			rpm = f.rpmSeq[f.rpmIdx]
			f.rpmIdx++
		} else if len(f.rpmSeq) > 0 {
			rpm = f.rpmSeq[len(f.rpmSeq)-1] // repeat last value when sequence exhausted
		}
		return []byte(strconv.Itoa(rpm) + "\n"), nil
	}
	// PWM read
	return []byte(strconv.Itoa(f.pwm) + "\n"), nil
}

// WriteFile simulates os.WriteFile on sysfs paths.
func (f *FakeHwmon) WriteFile(path string, data []byte, _ interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeFail {
		return fmt.Errorf("fake: write fail")
	}
	s := strings.TrimSpace(string(data))
	v, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("fake: invalid pwm value %q", s)
	}
	f.pwm = v
	f.writes = append(f.writes, v)
	return nil
}

// Writes returns all PWM values written so far.
func (f *FakeHwmon) Writes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]int, len(f.writes))
	copy(cp, f.writes)
	return cp
}

// CurrentPWM returns the current simulated PWM value.
func (f *FakeHwmon) CurrentPWM() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pwm
}

// FakeNVML implements polarity.NVMLInterface for tests.
type FakeNVML struct {
	mu             sync.Mutex
	driverVersion  string
	fanSpeed       uint8
	policy         int
	setSpeedCalls  []uint8
	setPolicyCalls []int
	speedFail      bool
	policyFail     bool
	setSpeedFail   bool
}

// NewFakeNVML creates a FakeNVML with the given driver version, fan speed, and policy.
func NewFakeNVML(driverVersion string, fanSpeed uint8, policy int) *FakeNVML {
	return &FakeNVML{
		driverVersion: driverVersion,
		fanSpeed:      fanSpeed,
		policy:        policy,
	}
}

// SetSetSpeedFail makes subsequent SetFanSpeed calls return an error.
func (f *FakeNVML) SetSetSpeedFail(v bool) { f.setSpeedFail = v }

func (f *FakeNVML) DriverVersion() (string, error) {
	return f.driverVersion, nil
}

func (f *FakeNVML) GetFanSpeed(_ uint, _ int) (uint8, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.speedFail {
		return 0, fmt.Errorf("fake: speed read fail")
	}
	return f.fanSpeed, nil
}

func (f *FakeNVML) GetFanControlPolicy(_ uint, _ int) (int, bool, error) {
	if f.policyFail {
		return 0, false, fmt.Errorf("fake: policy read fail")
	}
	return f.policy, true, nil
}

func (f *FakeNVML) SetFanControlPolicy(_ uint, _ int, policy int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setPolicyCalls = append(f.setPolicyCalls, policy)
	f.policy = policy
	return true, nil
}

func (f *FakeNVML) SetFanSpeed(_ uint, _ int, pct uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setSpeedFail {
		return fmt.Errorf("fake: set speed fail")
	}
	f.setSpeedCalls = append(f.setSpeedCalls, pct)
	f.fanSpeed = pct
	return nil
}

// SetSpeedCalls returns the speed values passed to SetFanSpeed.
func (f *FakeNVML) SetSpeedCalls() []uint8 {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]uint8, len(f.setSpeedCalls))
	copy(cp, f.setSpeedCalls)
	return cp
}

// SetPolicyCalls returns the policy values passed to SetFanControlPolicy.
func (f *FakeNVML) SetPolicyCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]int, len(f.setPolicyCalls))
	copy(cp, f.setPolicyCalls)
	return cp
}

// HwmonProberFromFake builds a polarity.HwmonProber wired to a FakeHwmon.
// A step-advancing Now function (400ms per call) is injected so that
// readRPMMean executes exactly 2 iterations for BaselineWindow (1s) and 1
// iteration for RestoreDelay (500ms), consuming sequences predictably in tests.
func HwmonProberFromFake(f *FakeHwmon) *polarity.HwmonProber {
	base := time.Now()
	var calls int64
	return &polarity.HwmonProber{
		Clock:    func(time.Duration) {}, // instant clock
		ReadFile: f.ReadFile,
		WriteFile: func(path string, data []byte, _ os.FileMode) error {
			return f.WriteFile(path, data, nil)
		},
		Now: func() time.Time {
			i := atomic.AddInt64(&calls, 1) - 1
			return base.Add(time.Duration(i) * 400 * time.Millisecond)
		},
	}
}
