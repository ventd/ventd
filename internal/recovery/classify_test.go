package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture loads a textdata file from testdata/ and splits it into
// the (errString, journalLines) tuple the classifier consumes. Files
// whose name contains "_journal" are loaded as journal-only fixtures
// (errString empty); other files are loaded as Go-error-string
// fixtures (journal empty). This mirrors the two-channel input
// shape of Classify itself.
func fixture(t *testing.T, name string) (errStr string, journal []string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	body := strings.TrimSpace(string(data))
	if strings.Contains(name, "journal") {
		return "", strings.Split(body, "\n")
	}
	return body, nil
}

// errFromFixture wraps a fixture's text as a Go error so the
// classifier sees the same shape it'd see in production (where the
// caller wraps the original error chain via fmt.Errorf("%w", ...)).
func errFromFixture(t *testing.T, name string) error {
	t.Helper()
	s, _ := fixture(t, name)
	return errors.New(s)
}

// RULE-WIZARD-RECOVERY-01: Secure Boot signature failures classify
// to ClassSecureBoot — both via Go error (modprobe / insmod stderr)
// and via the kernel's "Loading of unsigned module" journal stamp.
func TestClassify_SecureBoot(t *testing.T) {
	t.Parallel()
	t.Run("from modprobe stderr", func(t *testing.T) {
		err := errFromFixture(t, "secure_boot.txt")
		got := Classify(PhaseInstallingDriver, err, nil)
		if got != ClassSecureBoot {
			t.Fatalf("got %q, want %q", got, ClassSecureBoot)
		}
	})
	t.Run("from journal", func(t *testing.T) {
		_, journal := fixture(t, "secure_boot_journal.txt")
		got := Classify(PhaseInstallingDriver,
			errors.New("modprobe failed"), journal)
		if got != ClassSecureBoot {
			t.Fatalf("got %q, want %q", got, ClassSecureBoot)
		}
	})
}

// RULE-WIZARD-RECOVERY-02: Missing-headers errors classify to
// ClassMissingHeaders, matching DKMS's package-name + path stamps.
func TestClassify_MissingHeaders(t *testing.T) {
	t.Parallel()
	err := errFromFixture(t, "missing_headers.txt")
	got := Classify(PhaseInstallingDriver, err, nil)
	if got != ClassMissingHeaders {
		t.Fatalf("got %q, want %q", got, ClassMissingHeaders)
	}
}

// RULE-WIZARD-RECOVERY-03: DKMS build failures (gcc / make) classify
// to ClassDKMSBuildFailed. Phase-gated to PhaseInstallingDriver so a
// generic "make: ***" elsewhere doesn't false-trigger.
func TestClassify_DKMSBuildFailed(t *testing.T) {
	t.Parallel()
	t.Run("during install phase", func(t *testing.T) {
		err := errFromFixture(t, "dkms_build_failed.txt")
		got := Classify(PhaseInstallingDriver, err, nil)
		if got != ClassDKMSBuildFailed {
			t.Fatalf("got %q, want %q", got, ClassDKMSBuildFailed)
		}
	})
	t.Run("outside install phase ⇒ unknown", func(t *testing.T) {
		err := errFromFixture(t, "dkms_build_failed.txt")
		got := Classify(PhaseRuntime, err, nil)
		if got != ClassUnknown {
			t.Fatalf("expected unknown for runtime phase, got %q", got)
		}
	})
}

// RULE-WIZARD-RECOVERY-04: AppArmor denials classify to
// ClassApparmorDenied. Both wizard and doctor surfaces hit this:
// wizard during driver-install helpers, doctor when an upgraded
// kernel changes the profile attach behaviour at runtime.
func TestClassify_ApparmorDenied(t *testing.T) {
	t.Parallel()
	_, journal := fixture(t, "apparmor_denied_journal.txt")
	got := Classify(PhaseInstallingDriver,
		errors.New("hwmon: install: modprobe failed"), journal)
	if got != ClassApparmorDenied {
		t.Fatalf("got %q, want %q", got, ClassApparmorDenied)
	}
	// Same fixture in PhaseRuntime — exercise the cross-cutting
	// classifier shape (doctor surface).
	got = Classify(PhaseRuntime,
		errors.New("monitor: read failed"), journal)
	if got != ClassApparmorDenied {
		t.Fatalf("PhaseRuntime: got %q, want %q", got, ClassApparmorDenied)
	}
}

// RULE-WIZARD-RECOVERY-05: Plain "Module not found" without a
// signing rejection classifies to ClassMissingModule.
func TestClassify_MissingModule(t *testing.T) {
	t.Parallel()
	err := errFromFixture(t, "missing_module.txt")
	got := Classify(PhaseInstallingDriver, err, nil)
	if got != ClassMissingModule {
		t.Fatalf("got %q, want %q", got, ClassMissingModule)
	}
}

// RULE-WIZARD-RECOVERY-06: Unrecognised errors fall through to
// ClassUnknown without crashing or panicking. Catches the regression
// class where a future regex change short-circuits the no-match path.
func TestClassify_UnknownFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{"nil error", nil},
		{"empty string", errors.New("")},
		{"unrelated text", errors.New("disk full")},
		{"random go panic", errors.New("runtime error: index out of range [5] with length 3")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(PhaseInstallingDriver, tc.err, nil)
			if got != ClassUnknown {
				t.Errorf("%s: got %q, want unknown", tc.name, got)
			}
		})
	}
}

// RULE-WIZARD-RECOVERY-07: Classifier must respect ordering — the
// secure-boot rule fires BEFORE missing-module/dkms-build because
// signature failures emit text that would otherwise trip those
// rules ("FATAL" appears in both signing rejections and missing-
// module errors).
func TestClassify_OrderingRespected(t *testing.T) {
	t.Parallel()
	// A real-world combined error: modprobe says FATAL but the
	// underlying cause is a signing rejection.
	combined := errors.New(
		"modprobe: FATAL: Module nct6687 not found: Key was rejected by service")
	got := Classify(PhaseInstallingDriver, combined, nil)
	if got != ClassSecureBoot {
		t.Fatalf("ordering broken — combined error classified as %q, want secure_boot", got)
	}
}

// RULE-WIZARD-RECOVERY-08: Wrapped errors (fmt.Errorf("%w", ...))
// surface their full chain to the classifier. The wizard wraps
// errors at every phase boundary; the classifier must see the
// inner-most cause string.
func TestClassify_WrappedErrors(t *testing.T) {
	t.Parallel()
	inner := errors.New("modprobe: ERROR: could not insert 'nct6687': Key was rejected by service")
	wrapped := fmt.Errorf("install_driver: %w", inner)
	wrappedTwice := fmt.Errorf("setup phase: %w", wrapped)
	got := Classify(PhaseInstallingDriver, wrappedTwice, nil)
	if got != ClassSecureBoot {
		t.Fatalf("wrapped error not classified — got %q", got)
	}
}

// RULE-WIZARD-RECOVERY-09: All declared FailureClass values appear
// TestClassify_ACPIResourceConflict pins the dual-signal gating
// for ClassACPIResourceConflict — bare ENODEV alone could mean "no
// IT chip present" (e.g. AMD board with no IT controller); pairing
// with the kernel's "ACPI: resource ... conflicts" stamp
// disambiguates "BIOS won't let us bind" from "no chip here". A
// regression that fires on bare ENODEV would falsely surface the
// auto-fix card on every host without an IT chip.
//
// Bound: RULE-WIZARD-RECOVERY-10 — ACPI resource conflict requires
// both modprobe ENODEV AND kernel ACPI conflict stamp.
func TestClassify_ACPIResourceConflict(t *testing.T) {
	t.Parallel()

	t.Run("both signals fire", func(t *testing.T) {
		err := errors.New("modprobe: ERROR: could not insert 'it87': No such device")
		journal := []string{
			"kernel: ACPI: resource it87 [io  0x290-0x297] conflicts with ACPI region MOTH",
			"systemd[1]: Started ventd.service",
		}
		got := Classify(PhaseInstallingDriver, err, journal)
		if got != ClassACPIResourceConflict {
			t.Fatalf("got %q, want %q", got, ClassACPIResourceConflict)
		}
	})

	t.Run("ENODEV alone without ACPI stamp does not fire", func(t *testing.T) {
		err := errors.New("modprobe: ERROR: could not insert 'it87': No such device")
		got := Classify(PhaseInstallingDriver, err, []string{"unrelated"})
		if got == ClassACPIResourceConflict {
			t.Fatalf("classified ENODEV-only as ACPI conflict")
		}
	})

	t.Run("ACPI stamp without ENODEV does not fire", func(t *testing.T) {
		err := errors.New("some other modprobe failure")
		journal := []string{"ACPI: resource conflicts with region MOTH"}
		got := Classify(PhaseInstallingDriver, err, journal)
		if got == ClassACPIResourceConflict {
			t.Fatalf("classified ACPI-only as ACPI conflict")
		}
	})
}

// RULE-WIZARD-RECOVERY-10: ThinkPad fan_control gate classifies to
// ClassThinkpadACPIDisabled — narrowed after upstream-research
// validation. The kernel's thinkpad_acpi driver refuses fan writes
// SILENTLY (-EPERM with no per-write printk; the init-time message
// is dbg_printk()-gated and absent on every stock distro kernel).
// So Classify can only catch userspace-tool error strings + the
// shape ventd's own pwm_enable write helper produces when wrapping
// EPERM. Canonical pre-emptive detection is a sysfs probe of
// /sys/module/thinkpad_acpi/parameters/fan_control — added in a
// follow-up PR (DetectThinkpadACPIDisabled in probe.go).
func TestClassify_ThinkpadACPIDisabled(t *testing.T) {
	t.Parallel()
	t.Run("thinkfan-style error string", func(t *testing.T) {
		// thinkfan's canonical error when fan_control=0 is set.
		// Verbatim from vmatare/thinkfan source / issues #45 + #94.
		err := errors.New("ERROR: Module thinkpad_acpi doesn't seem to support fan_control")
		got := Classify(PhaseScanningFans, err, nil)
		if got != ClassThinkpadACPIDisabled {
			t.Fatalf("got %q, want %q", got, ClassThinkpadACPIDisabled)
		}
	})
	t.Run("ventd pwm_enable wrap with thinkpad_acpi context", func(t *testing.T) {
		// Shape of the error ventd produces when writing pwm_enable
		// fails with EPERM and the helper notes thinkpad_acpi context.
		err := errors.New("thinkpad_acpi: cannot write to pwm — fan_control=0 in modprobe options")
		got := Classify(PhaseInstallingDriver, err, nil)
		if got != ClassThinkpadACPIDisabled {
			t.Fatalf("got %q, want %q", got, ClassThinkpadACPIDisabled)
		}
	})
	t.Run("kernel-only journal does NOT fire (silent EPERM)", func(t *testing.T) {
		// Negative test that pins the upstream-research finding:
		// the kernel's thinkpad_acpi driver does NOT emit a per-
		// write printk on EPERM. A journal containing only
		// "Disabling fan write commands" or similar dbg_printk
		// strings (which most stock distro kernels never even
		// produce) MUST NOT cause the classifier to misfire on
		// unrelated wizard failures.
		journal := []string{
			"thinkpad_acpi: ThinkPad ACPI Extras v0.26",
			"thinkpad_acpi: Disabling fan write commands",
		}
		err := errors.New("modprobe: FATAL: Module nct6687 not found in directory")
		got := Classify(PhaseInstallingDriver, err, journal)
		if got == ClassThinkpadACPIDisabled {
			t.Fatalf("kernel-only journal triggered ThinkPad class; should have returned %q (missing-module)", ClassMissingModule)
		}
	})
}

// in AllFailureClasses() in display order. Pin the contract so a
// future addition to the enum forces an update to the catalogue.
func TestAllFailureClasses_Complete(t *testing.T) {
	t.Parallel()
	want := []FailureClass{
		ClassSecureBoot,
		ClassMissingModule,
		ClassMissingHeaders,
		ClassDKMSBuildFailed,
		ClassApparmorDenied,
		ClassMissingBuildTools,
		ClassInTreeConflict,
		ClassDKMSStateCollision,
		ClassReadOnlyRootfs,
		ClassDiskFull,
		ClassPackageManagerBusy,
		ClassDaemonNotRoot,
		ClassContainerised,
		ClassConcurrentInstall,
		ClassACPIResourceConflict,
		ClassDriverWontBind,
		ClassVendorDaemonActive,
		ClassThinkpadACPIDisabled,
		ClassNixOSPathIgnored,
	}
	got := AllFailureClasses()
	if len(got) != len(want) {
		t.Fatalf("AllFailureClasses len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}
