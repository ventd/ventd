package smartmode_test

// Cross-spec integration test for spec-smart-mode.md §16 success criterion #1:
//
//   "A first-time user with hardware ventd has never seen can install ventd
//    and have it either (a) calibrate and control fans within ~10 minutes,
//    or (b) install as a sensors-only dashboard, or (c) refuse gracefully
//    with diagnostic — no fourth path."
//
// RULE-PROBE-04 unit-tests ClassifyOutcome on synthetic ProbeResult values.
// This test drives the actual Prober end-to-end against fs.FS fixtures
// covering every reasonable combination of (virt × container × sensors ×
// channels), walks the result through ClassifyOutcome, and asserts the
// outcome is always one of the three documented states. A fourth path —
// a new outcome value, a panic, or a returned error mistaken for a fourth
// state by callers — would violate the smart-mode constraint.

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/probe/fixtures"
)

// makeExecFn returns a stub probe.ExecFn that emits canned answers for
// systemd-detect-virt --vm and --container and "none" for everything else.
func makeExecFn(vmOut, containerOut string) probe.ExecFn {
	return func(_ context.Context, name string, args ...string) (string, error) {
		if name != "systemd-detect-virt" || len(args) < 1 {
			return "none", nil
		}
		switch args[0] {
		case "--vm":
			return vmOut, nil
		case "--container":
			return containerOut, nil
		}
		return "none", nil
	}
}

// stubWriteCheck always returns writable=true and never opens an fd.
func stubWriteCheck(writable bool) probe.WriteChecker {
	return func(string) bool { return writable }
}

// validOutcome reports whether o is exactly one of the three documented states.
// Adding a fourth Outcome const without updating this set would fail every row.
func validOutcome(o probe.Outcome) bool {
	switch o {
	case probe.OutcomeControl, probe.OutcomeMonitorOnly, probe.OutcomeRefuse:
		return true
	}
	return false
}

// TestSmartmode_NoFourthPath_FromProbeToOutcome drives Prober.Probe through
// every reasonable (virt × container × sensors × channels) combination and
// asserts the §16 #1 contract: the outcome is always one of three known
// values, never a fourth, never a panic, never a silent error.
func TestSmartmode_NoFourthPath_FromProbeToOutcome(t *testing.T) {
	type scenario struct {
		name string

		// Inputs.
		sysFS    fstest.MapFS
		procFS   fstest.MapFS
		rootFS   fstest.MapFS
		vmExec   string
		ctnrExec string

		// Expected classification — encodes the §3.2 priority chain.
		want probe.Outcome

		// wantVirt / wantContainer pin WHY a refuse fires. Lets the test
		// fail when the outcome is right but produced by the wrong cause
		// (e.g. container scenario refusing only because no sensors were
		// enumerated, masking a regression in container detection).
		wantVirt      bool
		wantContainer bool
	}

	// virtSysFS strikes 2 of 3 virt signals (DMI vendor + /sys/hypervisor).
	// Combined with --vm output != "none" that's 3 of 3 → Virtualised=true.
	virtSysFS := fstest.MapFS{
		"class/dmi/id/sys_vendor":   {Data: []byte("QEMU\n")},
		"class/dmi/id/product_name": {Data: []byte("Standard PC (Q35 + ICH9, 2009)\n")},
		"hypervisor/type":           {Data: []byte("kvm\n")},
		"class/hwmon/hwmon0/name":   {Data: []byte("nct6798\n")},
		// Even with hardware visible, virt status forces refuse.
		"class/hwmon/hwmon0/temp1_input": {Data: []byte("38000\n")},
		"class/hwmon/hwmon0/pwm1":        {Data: []byte("128\n")},
		"class/hwmon/hwmon0/pwm1_enable": {Data: []byte("1\n")},
		"class/hwmon/hwmon0/fan1_input":  {Data: []byte("1200\n")},
	}

	// dockerProcFS strikes the Docker container path: /proc/1/cgroup contains
	// "docker" → 1 source. Combined with /.dockerenv at rootFS → 2 sources,
	// reaching the threshold for Containerised=true (RULE-PROBE-03).
	dockerProcFS := fstest.MapFS{
		"1/cgroup": {Data: []byte("0::/docker/abc123\n")},
		"cpuinfo":  {Data: []byte("processor\t: 0\nmodel name\t: cpu\n\n")},
	}
	dockerRootFS := fstest.MapFS{
		".dockerenv": {Data: []byte("")},
		"sys":        {Mode: 0o755 | 1<<31}, // dir
	}

	cases := []scenario{
		{
			name:     "bare-metal_no-sensors_no-channels__refuse",
			sysFS:    fixtures.SysEmpty(),
			procFS:   fixtures.ProcForBareMetal(),
			rootFS:   fixtures.BareMetalRoot(),
			vmExec:   "none",
			ctnrExec: "none",
			want:     probe.OutcomeRefuse,
		},
		{
			name:     "bare-metal_sensors-only__monitor-only",
			sysFS:    fixtures.SysWithThermalOnly(),
			procFS:   fixtures.ProcForBareMetal(),
			rootFS:   fixtures.BareMetalRoot(),
			vmExec:   "none",
			ctnrExec: "none",
			want:     probe.OutcomeMonitorOnly,
		},
		{
			name:     "bare-metal_sensors+channels__control",
			sysFS:    fixtures.SysWithThermalAndPWM(),
			procFS:   fixtures.ProcForBareMetal(),
			rootFS:   fixtures.BareMetalRoot(),
			vmExec:   "none",
			ctnrExec: "none",
			want:     probe.OutcomeControl,
		},
		{
			// Virt detection beats hardware presence: even with sensors and a
			// PWM channel visible to ventd inside a VM, the wizard refuses.
			name:     "virt_with_hardware__refuse",
			sysFS:    virtSysFS,
			procFS:   fixtures.ProcForBareMetal(),
			rootFS:   fixtures.BareMetalRoot(),
			vmExec:   "kvm",
			ctnrExec: "none",
			want:     probe.OutcomeRefuse,
			wantVirt: true,
		},
		{
			// Container detection beats hardware presence too. The Docker
			// fixture uses cgroup v1 keyword (1 source) + /.dockerenv (2nd).
			name:          "container_with_hardware__refuse",
			sysFS:         fixtures.SysWithThermalAndPWM(),
			procFS:        dockerProcFS,
			rootFS:        dockerRootFS,
			vmExec:        "none",
			ctnrExec:      "docker",
			want:          probe.OutcomeRefuse,
			wantContainer: true,
		},
	}

	// Track which outcomes we exercised; the test fails closed if any of the
	// three documented states was never reached (would mean the table drifted
	// and stopped covering the spec).
	exercised := map[probe.Outcome]bool{}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Defensive: even an unexpected panic must surface as a test
			// failure, not a process abort that blanks the rest of the table.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Probe panicked: %v (smart-mode constraint forbids panic on first contact)", r)
				}
			}()

			p := probe.New(probe.Config{
				SysFS:      tc.sysFS,
				ProcFS:     tc.procFS,
				RootFS:     tc.rootFS,
				ExecFn:     makeExecFn(tc.vmExec, tc.ctnrExec),
				WriteCheck: stubWriteCheck(true),
			})
			r, err := p.Probe(context.Background())
			if err != nil {
				t.Fatalf("Probe returned error: %v (§16 #1 forbids fatal probe; refuse is structured, not errored)", err)
			}
			if r == nil {
				t.Fatal("Probe returned nil result (§16 #1: refuse is a value, not nil)")
			}

			got := probe.ClassifyOutcome(r)

			if !validOutcome(got) {
				t.Fatalf("ClassifyOutcome returned %v (int=%d), not in {Control, MonitorOnly, Refuse} — fourth-path violation",
					got, int(got))
			}
			if got != tc.want {
				t.Errorf("outcome = %v, want %v\n  Virtualised=%v Containerised=%v ThermalSources=%d ControllableChannels=%d",
					got, tc.want,
					r.RuntimeEnvironment.Virtualised,
					r.RuntimeEnvironment.Containerised,
					len(r.ThermalSources),
					len(r.ControllableChannels),
				)
			}
			if tc.wantVirt && !r.RuntimeEnvironment.Virtualised {
				t.Errorf("scenario expected Virtualised=true to drive refuse, got false")
			}
			if tc.wantContainer && !r.RuntimeEnvironment.Containerised {
				t.Errorf("scenario expected Containerised=true to drive refuse, got false")
			}
			exercised[got] = true
		})
	}

	// Coverage assertion: every outcome MUST be exercised at least once.
	// If a refactor accidentally drops a row that was the only producer of
	// (e.g.) MonitorOnly, this catches it before merge.
	t.Run("table_covers_all_three_outcomes", func(t *testing.T) {
		for _, want := range []probe.Outcome{
			probe.OutcomeRefuse,
			probe.OutcomeMonitorOnly,
			probe.OutcomeControl,
		} {
			if !exercised[want] {
				t.Errorf("no scenario produced outcome %v — table coverage gap", want)
			}
		}
	})
}
