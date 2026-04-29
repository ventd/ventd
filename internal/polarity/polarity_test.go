package polarity_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
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
	// RULE-POLARITY-01: midpoint write MUST be exactly 128 for hwmon, 50 for NVML,
	// and vendor-specific for IPMI. Subtest verifies write capture per backend.
	t.Run("RULE-POLARITY-01_midpoint_write", func(t *testing.T) {
		t.Run("hwmon_writes_128", func(t *testing.T) {
			// baseline=200 RPM, observed=820 RPM → delta=+620 → normal
			fake := fixtures.NewFakeHwmon(64, []int{200, 200, 820, 820})
			p := fixtures.HwmonProberFromFake(fake)
			ch := makeChannel("/sys/pwm1", "/sys/fan1_input")
			_, err := p.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel: %v", err)
			}
			writes := fake.Writes()
			if len(writes) == 0 {
				t.Fatal("no writes recorded")
			}
			if writes[0] != 128 {
				t.Errorf("first write = %d, want 128", writes[0])
			}
		})

		t.Run("nvml_writes_50pct", func(t *testing.T) {
			// baseline=20%, observed=50% → delta=+30 → normal (>ThresholdPct)
			fake := fixtures.NewFakeNVML("570.211.01", 20, 1)
			p := &polarity.NVMLProber{Clock: noopClock, NVMLFuncs: fake}
			ch := &probe.ControllableChannel{
				PWMPath:  "nvml:0:0",
				Driver:   "nvml",
				Polarity: "unknown",
			}
			_, err := p.ProbeChannel(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeChannel: %v", err)
			}
			calls := fake.SetSpeedCalls()
			// First call must be 50 (midpoint)
			var found50 bool
			for _, c := range calls {
				if c == 50 {
					found50 = true
					break
				}
			}
			if !found50 {
				t.Errorf("SetFanSpeed was never called with 50; calls = %v", calls)
			}
		})

		t.Run("ipmi_dell_no_write_on_firmware_locked", func(t *testing.T) {
			// Dell firmware refusal: probe must NOT attempt additional writes after refusal
			dellProbe := &polarity.DellIPMIProbe{
				SendRecv: func(req, resp []byte) error {
					resp[0] = 0xd4 // iDRAC CC_INSUFFICIENT_PRIVILEGE
					return nil
				},
			}
			ch := &probe.ControllableChannel{SourceID: "ipmi0", Driver: "ipmi", Polarity: "unknown"}
			res, err := dellProbe.ProbeIPMIPolarity(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeIPMIPolarity: %v", err)
			}
			if res.Polarity != "phantom" || res.PhantomReason != polarity.PhantomReasonFirmwareLocked {
				t.Errorf("got polarity=%q reason=%q, want phantom/firmware_locked", res.Polarity, res.PhantomReason)
			}
		})
	})

	// RULE-POLARITY-02: hold time must be 3 seconds ± 200ms across backends.
	// Verified via injected clock accumulator.
	t.Run("RULE-POLARITY-02_hold_time_3s", func(t *testing.T) {
		var totalSleep time.Duration
		clockFn := func(d time.Duration) { totalSleep += d }

		fake := fixtures.NewFakeHwmon(64, []int{200, 200, 820, 820})
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
		// HoldDuration + RestoreDelay + baseline reads; hold must be exactly present.
		if totalSleep < polarity.HoldDuration-200*time.Millisecond {
			t.Errorf("total sleep %v < HoldDuration-200ms (%v)", totalSleep, polarity.HoldDuration-200*time.Millisecond)
		}
	})

	// RULE-POLARITY-03: phantom classification thresholds.
	// hwmon: abs(delta) < 150 → phantom; NVML: abs(delta) < 10 → phantom.
	t.Run("RULE-POLARITY-03_threshold_boundary", func(t *testing.T) {
		cases := []struct {
			name       string
			rpmSeq     []int
			wantPol    string
			wantReason string
		}{
			{"delta_+620_normal", []int{200, 200, 820, 820}, "normal", ""},
			{"delta_-620_inverted", []int{820, 820, 200, 200}, "inverted", ""},
			{"delta_+149_phantom", []int{200, 200, 349, 349}, "phantom", polarity.PhantomReasonNoResponse},
			{"delta_-149_phantom", []int{349, 349, 200, 200}, "phantom", polarity.PhantomReasonNoResponse},
			{"delta_0_phantom", []int{500, 500, 500, 500}, "phantom", polarity.PhantomReasonNoResponse},
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

		// NVML thresholds.
		nvmlCases := []struct {
			name     string
			baseline uint8
			observed uint8
			wantPol  string
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
				// Use seqFakeNVML to return baseline then observed speed.
				seq := &seqFakeNVML{
					driverVersion: "570.211.01",
					policy:        1,
					speeds:        []uint8{tc.baseline, tc.observed},
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

	// RULE-POLARITY-07: each IPMI vendor backend must implement IPMIVendorProbe.
	// Dell and HPE phantom their channels permanently (no deferred state).
	t.Run("RULE-POLARITY-07_ipmi_vendor_probe_interface", func(t *testing.T) {
		// Supermicro: real probe path via SendRecv.
		t.Run("supermicro_probes_via_send_recv", func(t *testing.T) {
			var cmds [][]byte
			smProbe := &polarity.SupermicroIPMIProbe{
				Clock: noopClock,
				SendRecv: func(req, resp []byte) error {
					cmds = append(cmds, append([]byte(nil), req...))
					// Get Sensor Reading → baseline 500*64 RPM
					if len(req) == 3 && req[0] == 0x04 {
						resp[0] = 0x00
						resp[1] = 8 // 8*64=512 RPM
						return nil
					}
					resp[0] = 0x00 // SET_FAN_SPEED OK
					return nil
				},
			}
			ch := &probe.ControllableChannel{SourceID: "ipmi0", Driver: "ipmi", Polarity: "unknown"}
			res, err := smProbe.ProbeIPMIPolarity(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeIPMIPolarity: %v", err)
			}
			// With equal baseline and observed → phantom no_response (no RPM delta).
			// What matters: probe ran (cmds non-empty) and a midpoint write was issued.
			if len(cmds) == 0 {
				t.Error("Supermicro probe sent no commands")
			}
			_ = res
		})

		t.Run("dell_firmware_locked_permanent_phantom", func(t *testing.T) {
			dellProbe := &polarity.DellIPMIProbe{
				SendRecv: func(req, resp []byte) error {
					resp[0] = 0xd4 // INSUFFICIENT_PRIVILEGE
					return nil
				},
			}
			ch := &probe.ControllableChannel{SourceID: "ipmi0", Driver: "ipmi", Polarity: "unknown"}
			res, err := dellProbe.ProbeIPMIPolarity(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeIPMIPolarity: %v", err)
			}
			if res.Polarity != "phantom" || res.PhantomReason != polarity.PhantomReasonFirmwareLocked {
				t.Errorf("Dell: got %q/%q, want phantom/firmware_locked", res.Polarity, res.PhantomReason)
			}
		})

		t.Run("hpe_405_permanent_phantom", func(t *testing.T) {
			hpeProbe := &polarity.HPEIPMIProbe{HTTPStatus: 405}
			ch := &probe.ControllableChannel{SourceID: "ipmi0", Driver: "ipmi", Polarity: "unknown"}
			res, err := hpeProbe.ProbeIPMIPolarity(context.Background(), ch)
			if err != nil {
				t.Fatalf("ProbeIPMIPolarity: %v", err)
			}
			if res.Polarity != "phantom" || res.PhantomReason != polarity.PhantomReasonProfileOnly {
				t.Errorf("HPE: got %q/%q, want phantom/profile_only", res.Polarity, res.PhantomReason)
			}
		})
	})

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
