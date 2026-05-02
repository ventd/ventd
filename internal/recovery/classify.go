// Package recovery is the cross-cutting failure-classification +
// remediation catalogue for ventd. Per #800, the same classifier
// serves two distinct UIs:
//
//  1. **Wizard recovery** (during initial setup) — the calibration
//     error banner consumes Progress.FailureClass / Progress.Remediation
//     and renders actionable cards instead of a Go error string.
//  2. **Doctor surface** (runtime, post-install) — when issues arise
//     after the wizard completes (AppArmor starts denying after a
//     kernel update, sensor goes sentinel, fan stops responding to
//     PWM writes), the doctor view consumes the same classifier so
//     the recovery UI is consistent across the operator's lifetime.
//
// Both surfaces resolve to the same FailureClass enum + Remediation
// catalogue, so adding a new failure class only requires touching
// this package — both UIs pick up the new entry on the next build.
//
// classify.go — failure classifier. Maps a free-form Go error +
// phase label + last-N journal lines onto a closed FailureClass.
// The remediation catalogue lives in remediation.go.

package recovery

import (
	"regexp"
	"strings"
)

// FailureClass enumerates the wizard failures the classifier
// recognises. The closed set is the operator-facing contract — adding
// a class is a v0.5.x.y amendment, not a schema break.
type FailureClass string

const (
	// ClassUnknown is the safe fallback. Operators see the
	// generic "Send diagnostic bundle" remediation; the
	// classifier did not match any of the known classes.
	ClassUnknown FailureClass = ""

	// ClassSecureBoot covers `module signature verification
	// failed` / `Loading of unsigned module rejected by service`
	// errors. Remediation surfaces MOK enrollment + the option
	// to disable Secure Boot in firmware.
	ClassSecureBoot FailureClass = "secure_boot"

	// ClassMissingModule covers `modprobe: FATAL: Module ... not
	// found` — the module isn't installed (or isn't built for
	// the running kernel). Remediation surfaces the wizard's
	// own load-module endpoint plus a diag-bundle option.
	ClassMissingModule FailureClass = "missing_module"

	// ClassMissingHeaders covers DKMS / OOT-driver build failures
	// where the linux-headers / kernel-headers package is missing.
	// Remediation surfaces the existing
	// /api/v1/hwdiag/install-kernel-headers endpoint.
	ClassMissingHeaders FailureClass = "missing_headers"

	// ClassDKMSBuildFailed covers OOT-driver build failures whose
	// root cause is NOT a missing headers package — most often a
	// gcc / make compilation error against a kernel API change.
	// Remediation suggests installing dkms (if missing) plus a
	// diag bundle since the maintainers usually need to see the
	// build log to fix.
	ClassDKMSBuildFailed FailureClass = "dkms_build_failed"

	// ClassApparmorDenied covers `apparmor="DENIED"` lines in the
	// journal during driver install / module load. Remediation
	// surfaces the new /api/v1/hwdiag/load-apparmor endpoint that
	// reloads the shipped profile so the operator's local-AppArmor
	// policy stops blocking the wizard.
	ClassApparmorDenied FailureClass = "apparmor_denied"
)

// All returns the closed set in display order. Used by tests and the
// /api/v1/hwdiag/recovery-classes route (future) to enumerate the
// catalogue without hard-coding the list at every call site.
func AllFailureClasses() []FailureClass {
	return []FailureClass{
		ClassSecureBoot,
		ClassMissingModule,
		ClassMissingHeaders,
		ClassDKMSBuildFailed,
		ClassApparmorDenied,
	}
}

// Classify maps the wizard's failure context onto a FailureClass.
// Inputs:
//   - phase: the wizard phase from Progress.Phase ("installing_driver",
//     "scanning_fans", etc.) — disambiguates errors with overlapping
//     text across phases.
//   - err: the Go error returned by the failing phase. nil ⇒ ClassUnknown.
//   - journal: last-N ventd journal lines, free-form. Match ordering is
//     "err string first, journal second" — explicit error wins.
//
// The classifier is deterministic, allocates only on regex match, and
// is hermetically testable (see classify_test.go for the fixture set).
//
// Order matters: the most-specific signatures fire first. Secure Boot
// signature failures look like generic modprobe failures unless we
// match the kernel's "Loading of unsigned module" / "key was rejected"
// stamps before the generic missing-module path.
func Classify(phase string, err error, journal []string) FailureClass {
	if err == nil {
		return ClassUnknown
	}
	msg := strings.ToLower(err.Error())
	joined := strings.ToLower(strings.Join(journal, "\n"))

	// 1. Secure Boot — strongest signal first because it produces
	// errors that overlap with both missing-module and DKMS-build
	// classes. Match on the kernel's signing-rejection stamps; both
	// `key was rejected` (errno -ENOKEY) and `signature verification
	// failed` are emitted by mod_verify_sig() in the kernel.
	if reSecureBoot.MatchString(msg) || reSecureBoot.MatchString(joined) {
		return ClassSecureBoot
	}
	// `Loading of unsigned module ... rejected` is the
	// systemd-journal-formatted form on enforcing distros.
	if reSecureBootJournal.MatchString(joined) {
		return ClassSecureBoot
	}

	// 2. AppArmor denials — specific journal stamp. Kernel emits
	// `apparmor="DENIED"` audit lines; we don't see these in the
	// Go error directly but they're in the journal.
	if reApparmor.MatchString(joined) || reApparmor.MatchString(msg) {
		return ClassApparmorDenied
	}

	// 3. Missing kernel headers — produces a build-time error from
	// dkms / make whose stderr names the missing path. This is a
	// specialisation of DKMS-build-failed, so it must match first.
	if reMissingHeaders.MatchString(msg) || reMissingHeaders.MatchString(joined) {
		return ClassMissingHeaders
	}

	// 4. DKMS build failed — gcc compilation error / `make: ***`
	// lines / `Bad return status`. Only fires during the
	// installing_driver phase.
	if phase == PhaseInstallingDriver &&
		(reDKMSBuild.MatchString(msg) || reDKMSBuild.MatchString(joined)) {
		return ClassDKMSBuildFailed
	}

	// 5. Missing module — `modprobe: FATAL: Module ... not found`,
	// `Module ... not found in directory`. Catch-all for non-signing
	// load failures.
	if reMissingModule.MatchString(msg) || reMissingModule.MatchString(joined) {
		return ClassMissingModule
	}

	return ClassUnknown
}

// Phase string constants. These cover both wizard phases (set by
// setup.Manager.SetPhase) and runtime contexts (set by doctor when
// classifying a runtime issue). Adding a phase is non-breaking;
// the classifier's phase-disambiguation rules are explicit.
const (
	PhaseDetecting        = "detecting"
	PhaseInstallingDriver = "installing_driver"
	PhaseScanningFans     = "scanning_fans"
	PhaseDetectingRPM     = "detecting_rpm"
	PhaseCalibrating      = "calibrating"
	// PhaseRuntime is the doctor-surface phase for issues that arise
	// after the wizard completes successfully. Used when the doctor
	// classifies a runtime sensor sentinel / AppArmor denial / fan
	// stall, so the same classifier handles both lifetimes.
	PhaseRuntime = "runtime"
)

// Compiled regex set. Each pattern is a stable substring of the
// kernel / userspace tool output the classifier matches. The
// fixtures in testdata/ exercise each class; if a pattern stops
// matching real-world output a regression test fails.
var (
	// kernel: "Loading of unsigned module is rejected" (when
	// CONFIG_MODULE_SIG_FORCE=y) or "module verification failed:
	// signature and/or required key missing" / "key was rejected
	// by service" (errno ENOKEY surfaced as -ENOKEY in modprobe).
	reSecureBoot = regexp.MustCompile(
		`(key was rejected by service|signature verification failed|module signature verification failed|enokey)`,
	)
	// systemd-journal stamps — "Loading of module ... rejected"
	// is emitted by load_module() in the kernel when sig
	// enforcement trips.
	reSecureBootJournal = regexp.MustCompile(
		`(loading of unsigned module|loading of module .* is rejected|module verification failed)`,
	)
	// AppArmor: kernel emits `apparmor="DENIED"` audit lines.
	// Userspace tools also log `apparmor: ... DENIED`.
	reApparmor = regexp.MustCompile(`apparmor="?denied"?`)
	// Missing kernel headers: dkms's build step looks for
	// `/lib/modules/$(uname -r)/build` which is a symlink into
	// the headers package; absence yields one of these stamps.
	// The pattern requires an absence-signalling phrase anchored
	// to the headers context — bare `linux-headers` / `kernel-headers`
	// would also match the Entering-directory line DKMS prints when
	// headers ARE installed (e.g. `Entering directory '/usr/src/linux-
	// headers-6.8.0-49-generic'`), and we don't want that to trip the
	// classifier. The chosen anchors are:
	//   * "kernel headers ... cannot be found / are missing / not found / not installed"
	//   * "install the (linux|kernel)-headers ..." (DKMS suggestion)
	//   * "/lib/modules/<ver>/build ... no such file / cannot be found"
	reMissingHeaders = regexp.MustCompile(
		`(kernel headers .* (cannot be found|are missing|not (found|installed))|install the linux-headers-?[0-9]|install the linux-headers package|install the kernel-headers|/lib/modules/[^/]+/build( |:)? *(no such file|not (found|exist)|cannot be found))`,
	)
	// DKMS build failure — gcc / make compilation errors.
	// `make: \*\*\*` is the canonical make-stop stamp.
	reDKMSBuild = regexp.MustCompile(
		`(make:\s+\*\*\*|compilation terminated|error: .* (undeclared|redeclared|incompatible)|bad return status for module build|dkms .* failed)`,
	)
	// Missing module — modprobe's `FATAL: Module not found`
	// or `Module ... not found in directory`. Note: the
	// Secure Boot check runs first because the kernel may
	// emit "FATAL" alongside a key-rejection stamp.
	reMissingModule = regexp.MustCompile(
		`(modprobe: ?fatal: ?module .* not found|module .* not found in directory|insmod: error inserting .*: -1 unknown symbol)`,
	)
)
