# Scenario 4 — Kernel too new

Host: `phoenix@192.168.7.217` (VM 203, Arch rolling, kernel `6.19.10-arch1-1`).

## Gap: no production driver currently sets `MaxSupportedKernel`

`hwmon.PreflightOOT` classifies as `KERNEL_TOO_NEW` when the running
kernel exceeds `DriverNeed.MaxSupportedKernel`. The code path is live in
`internal/hwmon/ootpreflight.go` and covered by the existing unit test
`TestPreflightOOT/kernel_too_new` plus the new
`internal/setup/preflight_diag_test.go::TestEmitPreflightDiag/kernel_too_new`.

However, **none** of the entries in `internal/hwmon/autoload.go`
`knownDriverNeeds` (`it8688e`, `it8689e`, `nct6687d`) currently populate
`MaxSupportedKernel`. That means the `KERNEL_TOO_NEW` diagnostic is
structurally correct but has no production trigger — a real user on a
bleeding-edge kernel will hit a build failure (`BUILD_FAILED`) rather
than the more informative kernel-too-new diagnostic.

This gap is now tracked in `HARDWARE-TODO.md` under *Code quality /
reliability backlog → Diagnostics accuracy*.

## In-matrix validation via synthetic ceiling

To prove classification works end-to-end on the Arch bleeding-edge kernel
target, `cmd/preflight-check` accepts `-max-kernel=X.Y` to inject a
synthetic ceiling into the `DriverNeed` it feeds to `PreflightOOT`.

### Evidence

Without ceiling (headers also absent on clean Arch — chain order
`SB → ceiling → headers → DKMS` correctly falls through to headers):

```json
{
  "detail": "Kernel headers for 6.19.10-arch1-1 are not installed. They are required to build the synthetic module.",
  "reason": 1,
  "reason_string": "KERNEL_HEADERS_MISSING"
}
```

With synthetic ceiling `-max-kernel=6.6`:

```json
{
  "detail": "Kernel 6.19.10-arch1-1 is newer than the last version SYNTHETIC is known to build against (6.6). The upstream driver has not been updated; build will likely fail.",
  "reason": 4,
  "reason_string": "KERNEL_TOO_NEW"
}
```

## Outcome

✓ `KERNEL_TOO_NEW` classified correctly when a ceiling is supplied; chain
precedence is correct on live Arch (falls through to headers when no
ceiling). **Production trigger gap documented and backlogged** — the
diagnostic will not fire for real users until at least one entry in
`knownDriverNeeds` receives a researched `MaxSupportedKernel` value.
