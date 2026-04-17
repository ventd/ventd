# Review T0-INFRA-01-R1
Task: T0-INFRA-01
PR: https://github.com/ventd/ventd/pull/239
Tip SHA: d9e2a8a80733e9fccb81703c8fbbb6bbb8067bf2
Verdict: revise (R7 — fakemic package misinterpretation)

Checklist:
  R1:  ✅ Draft, Task ID in body.
  R2:  ✅ Branch claude/INFRA-fixture-skeletons-4c9e2 matches assigned.
  R3:  ✅ Single commit, conventional-commits: "test: add fixture library skeleton (T0-INFRA-01)".
  R4:  ✅ Files touched == allowlist: testutil/ (2 files), internal/testfixture/** (28 files across 14 packages), CHANGELOG.md.
  R5:  ✅ Test files added are the T-task's scoped deliverable — 15 _test.go files (14 fixture TestNew + testutil has no tests). R5's intent is to prevent P-task drift; T-tasks invert it.
  R6:  ⏳ 8/13 CI lanes green on d9e2a8a (alpine, golangci-lint, shellcheck, cross-compile amd64+arm64, apparmor-parse-debian13, nix-drift, govulncheck). 5 in_progress (ubuntu, ubuntu-arm64, fedora, arch, headless-chromium). Trending green; all static-analysis complete.
  R7:  ❌ Partial content error. Thirteen of fourteen fixture packages interpret the fixture's future role correctly (testplan §3). fakemic is misinterpreted: CC read "mic" as microcontroller, not microphone.
       - Current package doc: "Package fakemic provides a deterministic microcontroller interface for unit tests."
       - Correct per testplan §3: "Signal-generator producing synthetic fan acoustics with configurable blade-pass + bearing defects." Consumed by internal/acoustic (future T-ACOUSTIC-01).
       - Current stub method: `func (f *Fake) Send() { f.rec.Record("Send") }`. "Send" is semantically wrong; a signal generator produces samples, not sends them.
       - The package name `fakemic` and its file paths stay correct — just the doc line and the method name need correcting.
       Minor non-finding: fakeliquid doc reads "liquid cooling monitor" where testplan §3 says "USB HID mock with Corsair Commander Core / NZXT X3 / Lian Li UNI Hub protocols." Understated but not wrong; not revision-worthy on its own.
  R8:  ✅ No new dependencies. go.mod unchanged.
  R9:  ✅ No safety-critical code paths touched.
  R10: ✅ No secrets / hardware fingerprints.
  R11: ✅ CHANGELOG entry added correctly beneath P0-03's line.
  R12: ✅ Public API addition is scoped to this task's deliverable (testutil + internal/testfixture/**); no retroactive API change.
  R13: ✅ Single track (INFRA).
  R14: ✅ golangci-lint green; go vet clean per CC's output.
  R15: n/a Not a binary-producing change.
  R16: ✅ No panic/log.Fatal/os.Exit added.
  R17: ✅ No goroutine added.
  R18: ✅ No file reader/writer added.
  R19: ✅ No `Fixes:` line → no regression test required.
  R20: n/a No safety-critical function added (future fakehwmon / fakeipmi impls will need to bind to their respective rule files; that's T0-INFRA-02 onward).
  R21: ✅ No goroutine added.
  R22: n/a This is the fixture library, not a backend. T-HAL-01 binds the HAL contract; that's a later T-task.
  R23: n/a No public metric, route, or config field added.

Notes:
  Fix is trivial — two lines in internal/testfixture/fakemic/fakemic.go
  (doc comment + method name and its Record() label). Revision round 1
  of 3. If the developer wants to skip the revision and accept the
  skeleton as-is on grounds that T-ACOUSTIC-01 will rewrite this file
  anyway, they can DROP T0-INFRA-01 or signal RESUME over the R7 fail;
  Cowork will not push an accept without the fix per §5 "ambiguous row is
  revise, not a judgement call" — though the fakemic miss is not
  ambiguous, it's a clear interpretation error.

  PHASE-T0/FIRST-PR gate (pre-announced) remains in effect for the
  accept step whenever this PR clears R7. Cowork will not auto-merge
  the first T-task PR without developer signal, whether or not the
  revision happens.
