package controller

import (
	"path/filepath"
	"testing"
	"time"
)

// TestTick_LowTempDisconnectCarriesForwardThenHandsBack drives the REAL control
// tick across a disconnected-thermistor event and pins the end-to-end half of
// RULE-CTRL-LOWTEMP-DISCONNECT (#1449). The existing unit test
// (TestReadAllSensors_LowTempDisconnectedFlaggedAsSentinel) only proves
// readAllSensors *flags* the bogus-low reading; this proves what the controller
// then DOES with the fan:
//
//   - a healthy reading establishes a last-good PWM;
//   - when the sensor drops to a disconnected ~8.5 °C, the tick CARRIES FORWARD
//     the last-good PWM instead of computing min PWM from the bogus-cold value
//     (the under-cool bug the rule exists to prevent);
//   - after the 30 s grace dwell, the fan is handed back to firmware auto
//     (pwm_enable=2), whose own working sensor reads the real temperature.
func TestTick_LowTempDisconnectCarriesForwardThenHandsBack(t *testing.T) {
	ff := newFakeFan(t)
	chipDir := filepath.Dir(ff.tempPath)
	// MinPWM=80 so "carry-forward last-good" is distinguishable from the
	// under-cool failure mode (which would clamp the bogus-cold curve to 80).
	cfg := makeLinearCurveCfg(ff, "cpu", "cpu_curve", 80, 255)
	c := newTestController(t, ff, cfg, &stubCal{}, "cpu", "cpu_curve")
	c.wd.Register(ff.pwmPath, "hwmon") // capture firmware enable (2) for the handback

	// 1) Healthy 70 °C → linear(70,40,80,0,255)=191. Establishes last-good.
	writeTempAttr(t, chipDir, "temp1_input", "70000")
	c.tick()
	good := readPWMByte(t, ff.pwmPath)
	if good <= 80 {
		t.Fatalf("healthy-tick PWM=%d, want >80 (so carry-forward is distinguishable from under-cool)", good)
	}

	// 2) Thermistor disconnects: 8.5 °C is below the ambient floor → data-loss.
	// The tick must carry forward the last-good PWM, NOT under-cool to min.
	writeTempAttr(t, chipDir, "temp1_input", "8500")
	c.tick()
	if got := readPWMByte(t, ff.pwmPath); got != good {
		t.Errorf("disconnected sensor: PWM=%d, want %d (carry-forward last-good, NOT min/under-cool)", got, good)
	}

	// 3) Disconnect persists past the 30 s grace → hand the fan to firmware auto.
	c.sensorInvalidSince["cpu"] = time.Now().Add(-sentinelInvalidDuration - time.Second)
	c.tick()
	if got := readIntFile(t, ff.enablePath); got != 2 {
		t.Errorf("after >30s disconnect: pwm_enable=%d, want 2 (handed back to firmware auto)", got)
	}
}
