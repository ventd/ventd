# Diagnostic-bundle rules (PR-2c)

These invariants govern the diagnostic-bundle generator
(`internal/diag/`), the redactor pipeline
(`internal/diag/redactor/`), and the observation-log NDJSON
exporter (the `ventd diag export-observations` CLI). Operator
trust in the bundle depends on these guarantees: default-
conservative redaction, fail-closed on self-check, mode 0600 on
mapping + bundle files, denylist coverage for shadow / SSH key /
raw-audio paths.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## RULE-DIAG-PR2C-01: Default redaction profile is default-conservative.

When `ventd diag bundle` is invoked without a `--redact` flag,
the redaction profile MUST be `default-conservative`. The
CLI's zero-value profile selector must resolve to
`default-conservative` without any additional configuration. A
bundle produced with the default profile must have
`"redactor_profile": "default-conservative"` in its
`REDACTION_REPORT.json`. This prevents accidental production
of un-redacted bundles when users run the command without
reading the help text — the sosreport failure mode of opt-in
redaction causing public disclosure.

Bound: internal/diag/redactor/redactor_test.go:default_profile_is_conservative

## RULE-DIAG-PR2C-02: Self-check pass detects un-redacted hostname strings in the final bundle.

After the bundle tarball is fully assembled, `SelfCheck` MUST
scan every file in the archive for the literal hostname string
(as returned by `os.Hostname()` at bundle-start time). If ANY
occurrence of the cleartext hostname is found in any file
(regardless of whether the redactor reported redacting it),
`SelfCheck` MUST return a non-nil error listing the file(s)
and byte offsets. The self-check is performed on the assembled
tarball content before the file handle is closed. A self-check
that passes when the hostname is present — e.g. because the
redactor missed a file, or a detection script added content
after the redactor ran — produces a bundle with a false
`redaction_consistent` flag and violates user trust.

Bound: internal/diag/redactor/redactor_test.go:self_check_detects_hostname_leak

## RULE-DIAG-PR2C-03: Self-check failure is fatal unless --allow-redaction-failures is passed.

When `SelfCheck` returns a non-nil error, `bundle.Generate`
MUST return that error to the caller and the bundle file MUST
be deleted (or never flushed to disk). The CLI command exits
non-zero with a message that names the leaking file(s). The
only override is `--allow-redaction-failures`, which causes
the error to be downgraded to a warning logged to stderr and
printed in `REDACTION_REPORT.json` under `"warnings"`. An
undetected or silently-swallowed self-check failure means the
user may share a bundle they believe is redacted when it is
not — the exact failure mode the self-check was designed to
prevent.

Bound: internal/diag/redactor/redactor_test.go:self_check_failure_is_fatal

## RULE-DIAG-PR2C-04: Redactor mapping file is created with mode 0600.

The persistent mapping store at
`/var/lib/ventd/redactor-mapping.json` (root-mode) or
`$XDG_STATE_HOME/ventd/redactor-mapping.json` (user-mode) MUST
be created with
`os.OpenFile(..., os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)`
and the mode MUST be verified via `os.Stat` after the file is
written and closed. A mapping file created with a permissive
umask (e.g. 0644) exposes the cleartext-to-obfuscated
mapping — which is the de-redaction key — to any user on the
system. The stat-verify step catches the failure class where a
caller creates the file first (0644 by default) and then
writes to it; `OpenFile` with explicit mode must be used from
the start, not chmod-after-write.

Bound: internal/diag/redactor/redactor_test.go:mapping_file_mode_0600

## RULE-DIAG-PR2C-05: --redact=off requires interactive confirmation or --i-understand-this-is-not-redacted.

When `--redact=off` is passed without
`--i-understand-this-is-not-redacted`, the CLI MUST prompt the
user interactively: print a warning message to stderr and read
from stdin. The bundle proceeds only if the user types the
exact word `confirm` (case-sensitive). Any other input
(including empty, ^C, or a mistyped word) MUST abort bundle
generation with exit code 1. When both `--redact=off` AND
`--i-understand-this-is-not-redacted` are present together,
the confirmation step is skipped and bundle generation
proceeds without any prompt. This is not a security gate — a
determined user can trivially bypass it — but a
deliberate-action ratchet that prevents accidentally-unredacted
bundles from being produced when the user simply forgot to
remove `--redact=off` from a debug session.

Bound: internal/diag/redactor/redactor_test.go:off_requires_confirm_or_flag

## RULE-DIAG-PR2C-06: Architecturally-excluded paths are never captured, even with --redact=off.

The paths listed in the capture denylist MUST never be read or
included in any bundle, regardless of redaction profile or CLI
flags. The denylist is hardcoded (not configurable) and
includes: `/etc/shadow`, `/etc/sudoers`, `/etc/sudoers.d/`,
SSH key files (`/root/.ssh/`, `/home/*/.ssh/`), TLS private
key files (`*.key`, `*.pem` containing `PRIVATE KEY`),
`/proc/<pid>/environ` for any process, shell history files
(`~/.bash_history`, `~/.zsh_history`), D-Bus session
credentials, and kernel keyring contents (`/proc/keys`). The
enforcement is architectural: the bundle generator's capture
allowlist does not include these paths. No redactor primitive
is asked to scrub them because they are never collected. The
test fixture verifies that an attempt to add a denylist path
via `detection.AddFile` returns `ErrDenied` regardless of the
redaction profile in effect.

Bound: internal/diag/bundle_test.go:denylist_paths_never_captured

## RULE-DIAG-PR2C-07: REDACTION_REPORT.json is generated for every bundle including --redact=off.

Every bundle MUST contain `REDACTION_REPORT.json` at the
tarball root. For `default-conservative` and
`trusted-recipient` profiles, the report contains per-class
redaction counts and `"redaction_consistent": true/false` from
the self-check result. For `--redact=off` bundles, the report
MUST still be generated and MUST contain
`"redactor_profile": "off"` and all class counts set to 0.
The report is generated after the self-check pass so it
reflects the final consistent state. A bundle without
`REDACTION_REPORT.json` cannot be audited by the user before
sharing and is rejected by the manifest validator. The test
fixture verifies that both a default-conservative bundle and
an off-profile bundle each contain a well-formed
`REDACTION_REPORT.json`.

Bound: internal/diag/bundle_test.go:redaction_report_always_present

## RULE-DIAG-PR2C-08: Redaction mapping is consistent within a bundle (same input → same output).

Within a single bundle generation run, each primitive that
uses consistent-mapping (P1 hostname, P3 MAC, P4 IP, P5
username) MUST map the same cleartext value to the same
obfuscated token every time it appears, across all files in
the bundle. The consistency is enforced by the shared mapping
store passed to every primitive. The test fixture feeds the
same hostname string to the P1 primitive from two different
simulated detection outputs and asserts that both are replaced
with the identical `obf_host_1` token. Inconsistent mapping
(e.g., two different tokens for the same hostname in different
files) destroys the analytical utility of the bundle — a
support engineer can no longer determine whether two
references point to the same machine.

Bound: internal/diag/redactor/redactor_test.go:mapping_consistent_within_bundle

## RULE-DIAG-PR2C-09: Reading a mapping file from another machine must not crash (graceful schema-mismatch).

When the mapping store attempts to load an existing
`redactor-mapping.json` and the file contains an unrecognised
schema version or malformed JSON, the load MUST succeed with
an empty mapping (discarding the file's contents) and log a
warning. The bundle generation continues with a fresh mapping
for this run. A crash (panic or returned fatal error) on a
foreign mapping file would prevent the daemon from generating
any bundle on machines where a mapping file was copied from
another host or corrupted by a partial write. The test fixture
constructs a mapping file with `"schema_version": 99` and an
unknown field and verifies that `MappingStore.Load` returns a
non-nil `*MappingStore` with empty maps and a logged warning,
not an error.

Bound: internal/diag/redactor/redactor_test.go:foreign_mapping_file_graceful

## RULE-DIAG-PR2C-10: Bundle output directory has mode 0o700; bundle file has mode 0o600. Both verified post-write.

The bundle output directory (resolved per §15.5 output-dir
precedence) MUST be created with `os.MkdirAll(dir, 0o700)`.
The bundle file MUST be created with
`os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)`.
After the file is written and closed, BOTH the directory mode
(via `os.Stat(dir).Mode().Perm()`) and the file mode (via
`os.Stat(path).Mode().Perm()`) MUST be verified to equal 0o700
and 0o600 respectively. A mismatch — possible when a
restrictive or permissive umask overrides the requested mode
on some filesystems — is treated as a fatal error and the
bundle file is removed. The test fixture creates the output in
a temp dir and asserts both stat results exactly.

Bound: internal/diag/bundle_test.go:output_dir_and_file_modes

## RULE-DIAG-PR2C-11: Raw audio temp files (`/tmp/ventd-acoustic-*`, `*.wav`, `*.raw`) are on the architectural denylist and never enter a bundle.

The v0.5.12 PR-D mic-calibration CLI
(`ventd calibrate --acoustic`) captures up to 60 s of mic
audio via ffmpeg → 16-bit PCM mono 48 kHz WAV → in-memory parse
for RMS dBFS + A-weighted dBFS + K_cal offset. The CLI
deletes the temp `.wav` immediately after parsing — raw audio
is never persisted by design.

**But** a crashed ffmpeg subprocess, a panic in the parser, or
an operator who stages a manual reference-tone capture by hand
could leave a stale audio file under `/tmp`. The architectural
denylist catches any such file before it could enter a
diagnostic bundle:

- `/tmp/ventd-acoustic-` — the canonical CLI temp prefix.
- `/tmp/ventd-mic-` — reserved for the operator-staged
  reference-tone capture path used by the wizard's PhaseGate
  (PR-D follow-up).
- `.wav` — generic suffix; catches operator-renamed captures
  anywhere.
- `.raw` — generic suffix; catches alternative encodings if a
  future collector emits raw PCM dumps.

Like all denylist entries (RULE-DIAG-PR2C-06), the enforcement
is architectural — the bundle generator simply never reads
files matching these patterns. No redactor primitive is asked
to scrub them because they are never collected.

The R30 calibration *result* (K_cal offset, dBA-vs-PWM curve,
mic identity hash) lives in the calibration JSON record under
`ChannelCalibration.{KCalOffset,DBAPerPWM,DBAPWMCurve,KCalMicID}`
and DOES enter the bundle — those fields are operator-meaningful
triage data with no embedded raw audio.

Bound: internal/diag/bundle_audio_denylist_test.go:TestRuleDiagPR2C_11_AudioTempFilesNeverCaptured

## RULE-DIAG-EXPORT-01: observation.Record round-trips through NDJSON envelope without field loss.

`runDiagExportObservations(args, logger)` MUST produce one
NDJSON line per record from the observation log. Each line is
a JSON object of the standard envelope shape:

```
{"schema_version": "1.0", "ts": "<RFC3339Nano>",
 "event_type": "observation_record",
 "payload": <observation.Record>}
```

The `payload` MUST decode back to a `*observation.Record`
whose `Ts`, `ChannelID`, `PWMWritten`, `RPM`,
`SignatureLabel`, `EventFlags`, and `SensorReadings` fields
are byte-equal to the original written record. The envelope's
`schema_version` MUST be exactly `"1.0"`
(`SchemaVentdJournalV1`); the `event_type` MUST be
`"observation_record"`.

A round-trip mismatch is a silent data-corruption regression
that would break any future `ventd doctor --watch` consumer or
maintainer-side ingest. The bound test seeds three records of
different shapes (with / without `EventFlags`, with / without
`SignatureLabel`, varying `SensorReadings`) and asserts each
round-trips field-equal.

Bound: cmd/ventd/diag_export_observations_test.go:TestDiagExportObservations_RoundTrip
