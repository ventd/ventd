package polarity_test

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/polarity/fixtures"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// openKV opens a fresh KVDB in a temp directory for polarity tests.
func openKV(t *testing.T) *state.KVDB {
	t.Helper()
	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.KV
}

func makeChannel(pwm, tach string) *probe.ControllableChannel {
	return &probe.ControllableChannel{
		SourceID: "hwmon0",
		PWMPath:  pwm,
		TachPath: tach,
		Driver:   "nct6775",
		Polarity: "unknown",
	}
}

func noopClock(time.Duration) {}

// ─── RULE-POLARITY-01 ────────────────────────────────────────────────────────

func TestPolarityRules(t *testing.T) {
	// RULE-POLARITY-01: bipolar probe MUST write BipolarLowPWM then
	// BipolarHighPWM for hwmon, and BipolarLowPct then BipolarHighPct for
	// NVML. The pre-#1110 midpoint-only write was replaced because it
	// misclassified normal fans whose baseline PWM was already above
	// midpoint (2026-05-15 NCT6687 wizard incident).
	t.Run("RULE-POLARITY-01_midpoint_write", func(t *testing.T) {
		t.Run("hwmon_writes_bipolar_low_then_high", func(t *testing.T) {
			// rpmSeq: [RPM_low=200, RPM_high=820] → delta=+620 → normal
			fake := fixtures.NewFakeHwmon(64, []int{200, 820})
			p := fixtures.HwmonProberFromFake(fake)
			ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
			_, err := p.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel: %v", err)
			}
			writes := fake.Writes()
			if len(writes) < 2 {
				t.Fatalf("want ≥2 writes (LOW, HIGH, restore); got %d: %v", len(writes), writes)
			}
			if writes[0] != int(polarity.BipolarLowPWM) {
				t.Errorf("first write = %d, want BipolarLowPWM (%d)", writes[0], polarity.BipolarLowPWM)
			}
			if writes[1] != int(polarity.BipolarHighPWM) {
				t.Errorf("second write = %d, want BipolarHighPWM (%d)", writes[1], polarity.BipolarHighPWM)
			}
		})

		t.Run("nvml_writes_bipolar_20_then_80", func(t *testing.T) {
			// LOW reads 20%, HIGH reads 80% → delta=+60 → normal
			seq := &seqFakeNVML{
				driverVersion: "570.211.01",
				policy:        1,
				speeds:        []uint8{20, 80},
			}
			p := &polarity.NVMLProber{Clock: noopClock, NVMLFuncs: seq}
			ch := &probe.ControllableChannel{
				PWMPath:  "nvml:0:0",
				Driver:   "nvml",
				Polarity: "unknown",
			}
			_, err := p.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel: %v", err)
			}
			calls := seq.setCalls
			if len(calls) < 2 {
				t.Fatalf("want ≥2 SetFanSpeed calls (LOW, HIGH); got %d: %v", len(calls), calls)
			}
			if calls[0] != polarity.BipolarLowPct {
				t.Errorf("first SetFanSpeed = %d, want BipolarLowPct (%d)", calls[0], polarity.BipolarLowPct)
			}
			if calls[1] != polarity.BipolarHighPct {
				t.Errorf("second SetFanSpeed = %d, want BipolarHighPct (%d)", calls[1], polarity.BipolarHighPct)
			}
		})

	})

	// RULE-POLARITY-02: bipolar pulse-hold ≥ BipolarPulseHold per pulse,
	// and BipolarPulseHold itself must be long enough to clear large-fan
	// spin-down inertia (issue #1221 — HIL on NCT6687 / 13900K box found
	// 2s was insufficient; 6s clears every measured channel).
	t.Run("RULE-POLARITY-02_hold_envelope", func(t *testing.T) {
		var totalSleep time.Duration
		clockFn := func(d time.Duration) { totalSleep += d }

		fake := fixtures.NewFakeHwmon(64, []int{200, 820})
		p := &polarity.HwmonProber{
			Clock:    clockFn,
			ReadFile: fake.ReadFile,
			WriteFile: func(path string, data []byte, _ os.FileMode) error {
				return fake.WriteFile(path, data, nil)
			},
		}
		ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
		if _, err := p.ProbeChannel(context.Background(), ch); err != nil {
			t.Fatalf("ProbeChannel: %v", err)
		}
		minPulseSleep := 2 * polarity.BipolarPulseHold
		if totalSleep < minPulseSleep-200*time.Millisecond {
			t.Errorf("total sleep %v < 2×BipolarPulseHold-200ms (%v)", totalSleep, minPulseSleep-200*time.Millisecond)
		}
		// Lock the minimum hold against regressions toward the
		// pre-#1221 2s value that produced false-phantoms.
		if polarity.BipolarPulseHold < 6*time.Second {
			t.Errorf("BipolarPulseHold = %v, want ≥ 6s for large-fan spin-down (issue #1221)",
				polarity.BipolarPulseHold)
		}
	})

	// RULE-POLARITY-02 regression: simulate a real NCT6687-class fan with
	// first-order spin-down inertia and verify the bipolar probe correctly
	// classifies it as "normal" rather than the false-phantom verdict the
	// pre-#1221 2s hold produced. Model parameters match the manual sweep
	// captured in issue #1221 on the 13900K / MSI Z690-A DDR4 / NCT6687D
	// board (τ_down ≈ 2.2s, τ_up ≈ 1.3s; PWM=51 → 660 RPM, PWM=204 →
	// 2400 RPM, BIOS baseline PWM=255 → 2900 RPM).
	t.Run("RULE-POLARITY-02_spindown_inertia_classifies_normal_1221", func(t *testing.T) {
		nowT := time.Unix(0, 0)
		lastWrite := 255
		writeAt := nowT
		const (
			tauDown = 2200 * time.Millisecond
			tauUp   = 1300 * time.Millisecond
		)
		// pwm → steady-state RPM table; values approximated from the
		// pwm8 channel in /root/sweep-nct6687-*.log captured for #1221.
		target := func(pwm int) float64 {
			switch {
			case pwm <= 0:
				return 0
			case pwm <= 51:
				return 660
			case pwm <= 204:
				return 2400
			default:
				return 2900
			}
		}
		rpmNow := func() int {
			tgt := target(lastWrite)
			// Use the slower of the two tau values when going down,
			// faster when going up — the asymmetry mimics the driver
			// torque-assisted spin-up and inertia-only spin-down.
			tau := tauDown
			prev := target(255)
			if tgt > prev {
				tau = tauUp
			}
			elapsed := nowT.Sub(writeAt)
			decay := math.Exp(-float64(elapsed) / float64(tau))
			return int(tgt + (prev-tgt)*decay)
		}

		readFile := func(path string) ([]byte, error) {
			if strings.Contains(path, "fan") {
				return []byte(strconv.Itoa(rpmNow()) + "\n"), nil
			}
			return []byte(strconv.Itoa(lastWrite) + "\n"), nil
		}
		writeFile := func(path string, data []byte, _ os.FileMode) error {
			v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			lastWrite = v
			writeAt = nowT
			return nil
		}

		p := &polarity.HwmonProber{
			Clock:     func(d time.Duration) { nowT = nowT.Add(d) },
			Now:       func() time.Time { return nowT },
			ReadFile:  readFile,
			WriteFile: writeFile,
		}
		ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
		res, err := p.ProbeChannel(context.Background(), ch)
		if err != nil {
			t.Fatalf("ProbeChannel: %v", err)
		}
		if res.Polarity != "normal" {
			t.Errorf("Polarity = %q (delta=%.0f, baseline=%.0f, observed=%.0f); want normal — issue #1221 regression",
				res.Polarity, res.Delta, res.Baseline, res.Observed)
		}
	})

	// RULE-POLARITY-03: phantom classification thresholds — bipolar delta.
	// rpmSeq[0]=RPM_low (after BipolarLowPWM), rpmSeq[1]=RPM_high (after
	// BipolarHighPWM). hwmon |delta| < 150 → phantom; sign disambiguates
	// normal vs inverted. NVML same with 10pct threshold.
	t.Run("RULE-POLARITY-03_threshold_boundary", func(t *testing.T) {
		cases := []struct {
			name       string
			rpmSeq     []int
			wantPol    string
			wantReason string
		}{
			{"delta_+620_normal", []int{200, 820}, "normal", ""},
			{"delta_-620_inverted", []int{820, 200}, "inverted", ""},
			{"delta_+149_phantom", []int{200, 349}, "phantom", polarity.PhantomReasonNoResponse},
			{"delta_-149_phantom", []int{349, 200}, "phantom", polarity.PhantomReasonNoResponse},
			{"delta_0_phantom", []int{500, 500}, "phantom", polarity.PhantomReasonNoResponse},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				fake := fixtures.NewFakeHwmon(64, tc.rpmSeq)
				p := fixtures.HwmonProberFromFake(fake)
				ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
				res, err := p.ProbeChannel(context.Background(), ch)
				if err != nil {
					t.Fatalf("ProbeChannel: %v", err)
				}
				if res.Polarity != tc.wantPol {
					t.Errorf("Polarity = %q, want %q (delta=%.0f)", res.Polarity, tc.wantPol, res.Delta)
				}
				if tc.wantReason != "" && res.PhantomReason != tc.wantReason {
					t.Errorf("PhantomReason = %q, want %q", res.PhantomReason, tc.wantReason)
				}
			})
		}

		// NVML thresholds (bipolar). speeds[0]=baselineSpeed read,
		// speeds[1]=LOW-pulse read, speeds[2]=HIGH-pulse read.
		nvmlCases := []struct {
			name    string
			low     uint8
			high    uint8
			wantPol string
		}{
			{"nvml_+30_normal", 20, 50, "normal"},
			{"nvml_-30_inverted", 50, 20, "inverted"},
			{"nvml_+9_phantom", 20, 29, "phantom"},
			{"nvml_-9_phantom", 29, 20, "phantom"},
			{"nvml_0_phantom", 50, 50, "phantom"},
		}
		for _, tc := range nvmlCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				// seqFakeNVML returns speeds in order on each GetFanSpeed.
				// Order: [baseline-read, low-pulse-read, high-pulse-read].
				seq := &seqFakeNVML{
					driverVersion: "570.211.01",
					policy:        1,
					speeds:        []uint8{tc.low, tc.low, tc.high},
				}
				p := &polarity.NVMLProber{Clock: noopClock, NVMLFuncs: seq}
				ch := &probe.ControllableChannel{
					PWMPath:  "nvml:0:0",
					Driver:   "nvml",
					Polarity: "unknown",
				}
				res, err := p.ProbeChannel(context.Background(), ch)
				if err != nil {
					t.Fatalf("ProbeChannel: %v", err)
				}
				if res.Polarity != tc.wantPol {
					t.Errorf("Polarity = %q, want %q (delta=%.0f)", res.Polarity, tc.wantPol, res.Delta)
				}
			})
		}
	})

	// RULE-POLARITY-13: bipolar probe must classify polarity invariant
	// to baseline PWM. The 2026-05-15 NCT6687 incident misclassified
	// every normal fan as inverted because the BIOS auto-curve had them
	// running at high PWM going into the probe and the midpoint write
	// produced a negative delta. This test pins the bug-fix contract.
	t.Run("RULE-POLARITY-13_bipolar_baseline_invariant", func(t *testing.T) {
		t.Run("hwmon_normal_fan_high_baseline_classifies_normal", func(t *testing.T) {
			// Fan running at PWM=255 / 2300 RPM going into the probe.
			// Bipolar probe drives LOW=51 then HIGH=204. Real normal
			// fan response: LOW yields ~400 RPM, HIGH yields ~1800 RPM.
			// delta = +1400 → normal. The pre-fix midpoint-vs-baseline
			// would have computed (1500-2300)=-800 and called this
			// inverted; the new algorithm cannot make that mistake.
			fake := fixtures.NewFakeHwmon(255, []int{400, 1800})
			p := fixtures.HwmonProberFromFake(fake)
			ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
			res, err := p.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel: %v", err)
			}
			if res.Polarity != "normal" {
				t.Errorf("Polarity = %q, want normal (delta=%.0f) — baseline-already-high regression",
					res.Polarity, res.Delta)
			}
			// Baseline PWM must be restored on the way out.
			if fake.CurrentPWM() != 255 {
				t.Errorf("baseline PWM not restored: got %d, want 255", fake.CurrentPWM())
			}
		})

		t.Run("hwmon_inverted_fan_low_baseline_classifies_inverted", func(t *testing.T) {
			// Inverted fan at PWM=10 (raw) is at high effective duty.
			// LOW=51 raw = high effective: ~1800 RPM.
			// HIGH=204 raw = low effective: ~400 RPM.
			// delta = -1400 → inverted.
			fake := fixtures.NewFakeHwmon(10, []int{1800, 400})
			p := fixtures.HwmonProberFromFake(fake)
			ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
			res, err := p.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel: %v", err)
			}
			if res.Polarity != "inverted" {
				t.Errorf("Polarity = %q, want inverted (delta=%.0f)", res.Polarity, res.Delta)
			}
		})
	})

	// RULE-POLARITY-04: probe MUST restore baseline on every exit path.
	t.Run("RULE-POLARITY-04_restore_on_all_paths", func(t *testing.T) {
		t.Run("hwmon_write_fail_restores_baseline", func(t *testing.T) {
			fake := fixtures.NewFakeHwmon(64, []int{200, 200})
			fake.SetWriteFail(true)
			p := fixtures.HwmonProberFromFake(fake)
			ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
			res, _ := p.ProbeChannel(context.Background(), ch)
			// Write fail path → phantom write_failed; baseline not changed.
			if res.Polarity != "phantom" {
				t.Errorf("want phantom on write fail, got %q", res.Polarity)
			}
		})

		t.Run("hwmon_context_cancel_restores", func(t *testing.T) {
			fake := fixtures.NewFakeHwmon(64, []int{200, 200})
			// Use a real clock so we can cancel mid-hold.
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			// The HwmonProber checks ctx before the hold sleep, so writes must still
			// restore. Use a clock that cancels mid-sleep.
			p := &polarity.HwmonProber{
				Clock:    noopClock,
				ReadFile: fake.ReadFile,
				WriteFile: func(path string, data []byte, _ os.FileMode) error {
					return fake.WriteFile(path, data, nil)
				},
			}
			ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
			_, err := p.ProbeChannel(ctx, ch)
			// Context cancel is expected.
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("unexpected error: %v", err)
			}
			// After cancel, pwm should be back to 64 (baseline).
			if got := fake.CurrentPWM(); got != 64 {
				t.Errorf("after cancel, PWM = %d, want 64 (baseline)", got)
			}
		})

		t.Run("nvml_restores_policy_and_speed", func(t *testing.T) {
			fake := fixtures.NewFakeNVML("570.211.01", 30, 1 /* auto */)
			p := &polarity.NVMLProber{Clock: noopClock, NVMLFuncs: fake}
			ch := &probe.ControllableChannel{PWMPath: "nvml:0:0", Driver: "nvml", Polarity: "unknown"}
			_, _ = p.ProbeChannel(context.Background(), ch)
			// Last SetFanControlPolicy call must restore original policy (1 = auto).
			pols := fake.SetPolicyCalls()
			if len(pols) < 2 {
				t.Fatalf("expected ≥2 policy calls (set manual + restore auto), got %d", len(pols))
			}
			if last := pols[len(pols)-1]; last != 1 {
				t.Errorf("last policy = %d, want 1 (auto/restore)", last)
			}
		})
	})

	// RULE-POLARITY-05: WritePWM MUST refuse phantom and unknown channels.
	t.Run("RULE-POLARITY-05_write_helper_refuses_phantom_unknown", func(t *testing.T) {
		noop := func(uint8) error { return nil }

		chPhantom := &probe.ControllableChannel{Polarity: "phantom"}
		if err := polarity.WritePWM(chPhantom, 128, noop); !errors.Is(err, polarity.ErrChannelNotControllable) {
			t.Errorf("phantom: want ErrChannelNotControllable, got %v", err)
		}

		chUnknown := &probe.ControllableChannel{Polarity: "unknown"}
		if err := polarity.WritePWM(chUnknown, 128, noop); !errors.Is(err, polarity.ErrPolarityNotResolved) {
			t.Errorf("unknown: want ErrPolarityNotResolved, got %v", err)
		}

		// Normal channel writes value unchanged.
		var written uint8
		chNormal := &probe.ControllableChannel{Polarity: "normal"}
		if err := polarity.WritePWM(chNormal, 200, func(v uint8) error { written = v; return nil }); err != nil {
			t.Fatalf("normal write: %v", err)
		}
		if written != 200 {
			t.Errorf("normal: written=%d, want 200", written)
		}

		// Inverted channel inverts the value.
		chInverted := &probe.ControllableChannel{Polarity: "inverted"}
		if err := polarity.WritePWM(chInverted, 200, func(v uint8) error { written = v; return nil }); err != nil {
			t.Fatalf("inverted write: %v", err)
		}
		if written != 55 { // 255 - 200
			t.Errorf("inverted: written=%d, want 55 (255-200)", written)
		}
	})

	// RULE-POLARITY-06: NVML probe must verify driver ≥ R515 before writing.
	// Older driver → phantom driver_too_old, no write attempted.
	t.Run("RULE-POLARITY-06_nvml_driver_version_gate", func(t *testing.T) {
		fake := fixtures.NewFakeNVML("510.108.03", 30, 1)
		p := &polarity.NVMLProber{Clock: noopClock, NVMLFuncs: fake}
		ch := &probe.ControllableChannel{PWMPath: "nvml:0:0", Driver: "nvml", Polarity: "unknown"}
		res, err := p.ProbeChannel(context.Background(), ch)
		if err != nil {
			t.Fatalf("ProbeChannel: %v", err)
		}
		if res.Polarity != "phantom" || res.PhantomReason != polarity.PhantomReasonDriverTooOld {
			t.Errorf("got polarity=%q reason=%q, want phantom/driver_too_old", res.Polarity, res.PhantomReason)
		}
		// Must not have called SetFanSpeed.
		if calls := fake.SetSpeedCalls(); len(calls) != 0 {
			t.Errorf("SetFanSpeed called %d times on old driver; want 0", len(calls))
		}
	})

	// RULE-POLARITY-07 is documentation-only in v0.5.39+ — the IPMI
	// vendor-dispatch surface (`internal/polarity/ipmi.go`) was deleted as
	// part of #1071's option-2 path because no production caller ever
	// constructed any of the vendor probes. The rule file is preserved as
	// a v0.7+ reservation; no bound subtest exists until the wiring is
	// brought back into scope.

	// RULE-POLARITY-08: daemon start applies persisted polarity by (backend,identity) key.
	// Miss → NeedsProbe=true; match → polarity applied; orphan → logged, no panic.
	t.Run("RULE-POLARITY-08_daemon_start_match", func(t *testing.T) {
		db := openKV(t)

		// Persist a normal result for pwm1.
		results := []polarity.ChannelResult{
			{
				Backend:  "hwmon",
				Identity: polarity.Identity{PWMPath: "/sys/pwm1"},
				Polarity: "normal",
				ProbedAt: time.Now(),
			},
		}
		if err := polarity.Persist(db, results); err != nil {
			t.Fatalf("Persist: %v", err)
		}

		// Live channels: pwm1 (matches) + pwm2 (miss = new channel).
		ch1 := makeChannel("/sys/pwm1", "/sys/fan1_input")
		ch2 := makeChannel("/sys/pwm2", "/sys/fan2_input")
		channels := []*probe.ControllableChannel{ch1, ch2}

		_, startResults, err := polarity.ApplyOnStart(db, channels, slog.Default(), time.Now())
		if err != nil {
			t.Fatalf("ApplyOnStart: %v", err)
		}

		// ch1 must be applied.
		if ch1.Polarity != "normal" {
			t.Errorf("ch1: Polarity=%q, want normal", ch1.Polarity)
		}

		// Find start result for ch2.
		var ch2Result *polarity.StartResult
		for i := range startResults {
			if startResults[i].Channel == "/sys/pwm2" {
				ch2Result = &startResults[i]
				break
			}
		}
		if ch2Result == nil {
			t.Fatal("no StartResult for ch2")
		}
		if !ch2Result.NeedsProbe {
			t.Error("ch2: NeedsProbe should be true for new channel")
		}
	})

	// RULE-POLARITY-09: reset wipes calibration namespace in addition to wizard+probe.
	t.Run("RULE-POLARITY-09_reset_wipes_calibration_namespace", func(t *testing.T) {
		db := openKV(t)

		// Persist polarity state.
		if err := polarity.Persist(db, []polarity.ChannelResult{
			{Backend: "hwmon", Identity: polarity.Identity{PWMPath: "/sys/pwm1"}, Polarity: "normal"},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}

		// Verify it's there.
		store, err := polarity.Load(db)
		if err != nil || store == nil {
			t.Fatalf("Load before wipe: store=%v err=%v", store, err)
		}

		// Wipe (calls probe.WipeNamespaces which now includes calibration).
		if err := wipeAll(db); err != nil {
			t.Fatalf("wipeAll: %v", err)
		}

		// Calibration namespace must be empty.
		storeAfter, err := polarity.Load(db)
		if err != nil {
			t.Fatalf("Load after wipe: %v", err)
		}
		if storeAfter != nil {
			t.Errorf("calibration namespace not empty after wipe; got %+v", storeAfter)
		}
	})

	// RULE-POLARITY-10: phantom channels MUST NOT be writable via WritePWM.
	// Control loop must treat phantom channels as monitor-only across all backends.
	t.Run("RULE-POLARITY-10_phantom_not_writable", func(t *testing.T) {
		phantomReasons := []string{
			polarity.PhantomReasonNoTach,
			polarity.PhantomReasonNoResponse,
			polarity.PhantomReasonFirmwareLocked,
			polarity.PhantomReasonProfileOnly,
			polarity.PhantomReasonDriverTooOld,
			polarity.PhantomReasonWriteFailed,
		}
		for _, reason := range phantomReasons {
			reason := reason
			t.Run(reason, func(t *testing.T) {
				ch := &probe.ControllableChannel{
					Polarity:      "phantom",
					PhantomReason: reason,
				}
				err := polarity.WritePWM(ch, 128, func(uint8) error { return nil })
				if !errors.Is(err, polarity.ErrChannelNotControllable) {
					t.Errorf("phantom(%s): want ErrChannelNotControllable, got %v", reason, err)
				}
				if polarity.IsControllable(ch) {
					t.Errorf("phantom(%s): IsControllable should be false", reason)
				}
			})
		}
	})
}

// wipeAll exercises probe.WipeNamespaces to also clear the calibration namespace
// (RULE-POLARITY-09). It uses the KV directly to delete calibration keys.
func wipeAll(db *state.KVDB) error {
	keys, err := db.List("calibration")
	if err != nil {
		return err
	}
	return db.WithTransaction(func(tx *state.KVTx) error {
		for k := range keys {
			tx.Delete("calibration", k)
		}
		return nil
	})
}

// ─── seqFakeNVML ─────────────────────────────────────────────────────────────

// seqFakeNVML returns a sequence of fan speeds on successive GetFanSpeed calls.
type seqFakeNVML struct {
	driverVersion string
	policy        int
	speeds        []uint8
	speedIdx      int
	setCalls      []uint8
	polCalls      []int
}

func (s *seqFakeNVML) DriverVersion() (string, error) { return s.driverVersion, nil }
func (s *seqFakeNVML) GetFanSpeed(_ uint, _ int) (uint8, error) {
	if s.speedIdx < len(s.speeds) {
		v := s.speeds[s.speedIdx]
		s.speedIdx++
		return v, nil
	}
	return s.speeds[len(s.speeds)-1], nil
}
func (s *seqFakeNVML) GetFanControlPolicy(_ uint, _ int) (int, bool, error) {
	return s.policy, true, nil
}
func (s *seqFakeNVML) SetFanControlPolicy(_ uint, _ int, policy int) (bool, error) {
	s.polCalls = append(s.polCalls, policy)
	s.policy = policy
	return true, nil
}
func (s *seqFakeNVML) SetFanSpeed(_ uint, _ int, pct uint8) error {
	s.setCalls = append(s.setCalls, pct)
	return nil
}
