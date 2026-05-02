# Wizard recovery classifier rules — v0.5.9 PR-C (#800)

These invariants govern the cross-cutting failure-classification +
remediation catalogue in `internal/recovery/`. The classifier serves
two distinct UIs:

1. **Wizard recovery** — the calibration error banner consumes
   `Progress.FailureClass` + `Progress.Remediation` (set in
   `internal/setup/setup.go::Status`) to render actionable cards
   instead of a Go error string.
2. **Doctor surface** (post-install) — runtime issues hit the same
   classifier when AppArmor starts denying after a kernel update,
   sensor goes sentinel, etc. (Wired into v0.5.10 doctor work.)

Both surfaces resolve to the same `FailureClass` enum + `Remediation`
catalogue, so adding a new failure class only requires touching this
package — both UIs pick up the new entry on the next build.

The patch spec is `specs/spec-wizard-recovery.md` (drafted alongside
this rule file). Each rule binds 1:1 to a subtest in
`internal/recovery/classify_test.go`. `tools/rulelint` blocks the
merge if a rule lacks its bound test.

## RULE-WIZARD-RECOVERY-01: Secure Boot signature failures classify to ClassSecureBoot.

Classifier matches both the modprobe / insmod stderr ("key was
rejected by service", "signature verification failed") and the
kernel's journal stamps ("Loading of unsigned module is rejected",
"module verification failed"). Test feeds both fixture shapes
(`testdata/secure_boot.txt`, `testdata/secure_boot_journal.txt`).

The Secure Boot rule fires BEFORE the missing-module rule because
signing rejections also emit "FATAL: Module not found"-shaped text
that would otherwise trip the missing-module classifier.

Bound: internal/recovery/classify_test.go:TestClassify_SecureBoot

## RULE-WIZARD-RECOVERY-02: Missing-headers errors classify to ClassMissingHeaders.

Pattern matches DKMS's package-name + path stamps when the kernel
headers package is absent: `kernel headers ... cannot be found`,
`install the linux-headers-...`, `/lib/modules/<ver>/build: no
such file`. The pattern explicitly EXCLUDES bare `linux-headers` /
`kernel-headers` mentions (DKMS prints the headers package's build
path even when headers ARE installed) — the regex requires an
absence-signalling phrase anchored to the headers context.

Bound: internal/recovery/classify_test.go:TestClassify_MissingHeaders

## RULE-WIZARD-RECOVERY-03: DKMS build failures classify to ClassDKMSBuildFailed and are phase-gated to PhaseInstallingDriver.

`make: ***`, gcc compilation errors (`undeclared`, `redeclared`,
`incompatible`), `bad return status for module build`, `dkms ...
failed`. The class only fires during the wizard's
`installing_driver` phase to avoid false-triggering on generic
make output that may appear elsewhere. Outside that phase the
classifier returns `ClassUnknown` and the operator gets the
generic diag-bundle remediation.

Bound: internal/recovery/classify_test.go:TestClassify_DKMSBuildFailed

## RULE-WIZARD-RECOVERY-04: AppArmor denials classify to ClassApparmorDenied across both wizard and doctor lifetimes.

Kernel emits `apparmor="DENIED"` audit lines on both phases. The
test exercises both `PhaseInstallingDriver` (wizard, fires when
driver-install helpers like `modprobe` or `apt-get` are blocked
by the profile) and `PhaseRuntime` (doctor, fires when an upgraded
kernel changes the profile attach behaviour) — same fixture, same
class. This is the canonical cross-cutting test.

Bound: internal/recovery/classify_test.go:TestClassify_ApparmorDenied

## RULE-WIZARD-RECOVERY-05: Plain "Module not found" without a signing rejection classifies to ClassMissingModule.

`modprobe: FATAL: Module ... not found in directory`,
`Module ... not found`, `insmod: error inserting ...`. Catch-all
for non-signing module load failures. The Secure Boot rule
(RULE-WIZARD-RECOVERY-01) fires first so a signing rejection
that also emits "FATAL" doesn't get mis-classified.

Bound: internal/recovery/classify_test.go:TestClassify_MissingModule

## RULE-WIZARD-RECOVERY-06: Unrecognised errors fall through to ClassUnknown without panicking.

Catches the regression class where a future regex change short-
circuits the no-match path. Tests cover nil error, empty string,
unrelated text ("disk full"), and a synthetic Go runtime panic
string. All five must produce `ClassUnknown`, never a panic.

Bound: internal/recovery/classify_test.go:TestClassify_UnknownFallback

## RULE-WIZARD-RECOVERY-07: Classifier ordering is preserved — Secure Boot fires before missing-module on combined errors.

Real-world combined error: `modprobe: FATAL: Module nct6687 not
found: Key was rejected by service`. The "FATAL" + "not found"
text alone would match `ClassMissingModule`, but the trailing
"Key was rejected" stamp signals signing rejection. Pinned so a
future re-ordering of classifier rules (or a regex change that
short-circuits) fails CI rather than silently regressing.

Bound: internal/recovery/classify_test.go:TestClassify_OrderingRespected

## RULE-WIZARD-RECOVERY-08: Wrapped errors (fmt.Errorf("%w", ...)) classify on the full chain.

The wizard wraps errors at every phase boundary (`fmt.Errorf("install
phase: %w", innerErr)`); the classifier must see the inner-most
cause string. Test wraps a Secure Boot error twice and confirms
classification still resolves to `ClassSecureBoot`. Go's
`fmt.Errorf("%w", ...)` serialises the wrapped chain into a single
string, so the classifier's `err.Error()` call sees the full text.

Bound: internal/recovery/classify_test.go:TestClassify_WrappedErrors

## RULE-WIZARD-RECOVERY-09: AllFailureClasses() enumerates every declared FailureClass in display order.

Pinned so a future addition to the enum forces an update to the
catalogue. The display order matters for UIs that render multiple
remediations side-by-side: ClassSecureBoot first (most-blocking),
ClassMissingModule, ClassMissingHeaders, ClassDKMSBuildFailed,
ClassApparmorDenied. ClassUnknown is intentionally excluded — it's
the fallback, not a catalogue entry.

Bound: internal/recovery/classify_test.go:TestAllFailureClasses_Complete
