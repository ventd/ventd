# RULE-SOAK-EXCITATION-OPT-IN: cmd/ventd-soak's synthetic-excitation driver is gated behind --enable-soak-excitation, never default, never shipped as production behaviour.

The Phase C v0.6.0 ship plan (at `/root/.claude/plans/you-are-a-30-vivid-pascal.md`)
identified that Phoenix's MSI Z690-A desktop ran v0.5.26 for 5 days with
8 Layer-B shards persisted but `theta=[0,0]` across every channel —
Layer-B never converged because the static-PWM workload did not satisfy
RULE-CMB-OAT-01's Δpwm-on-i-while-zero-on-j admissibility requirement.

`cmd/ventd-soak` is the field-validation harness that closes that gap.
It has two strictly-separated modes:

**Read-only observer mode** — `snapshot`, `watch`, `observations`
subcommands. Pure file-system reader of `$STATE_DIR/smart/shard-B/*.cbor`
via msgpack-decode of the `coupling.Bucket` shape. Opens no sysfs path,
makes no network call, talks to no daemon. Safe to run alongside the
production daemon — the daemon owns Save; the tool only Reads. This
mode is the default and ships in v0.5.36+.

**Excitation-driver mode** — `excite` subcommand. DRIVES synthetic Δpwm
steps on idle channels at 30-90s intervals to provide RLS excitation,
records pre/post Layer-B `Snapshot.Theta` + `Snapshot.P` + `Snapshot.Kappa`,
asserts identifiability per RULE-CPL-IDENT-02. This mode is gated:

1. **CLI flag**: requires `--enable-soak-excitation` on the command line
   to admit any code path that issues a PWM write. The flag's name is
   intentionally verbose so it cannot be accidentally typed; a short flag
   would tempt scripts to add it without operator awareness.

2. **Never default**: there is no config file, environment variable, or
   build tag that flips the gate on. The only way to admit excitation
   is to type the flag explicitly.

3. **Never production**: the harness is shipped as a separate binary
   (`cmd/ventd-soak/`) under the same module so it inherits the
   reproducible-build contract. It is NOT installed by the .deb / .rpm /
   AUR packages; operators run it from a checkout when needed. The
   production daemon (`cmd/ventd/`) never imports `cmd/ventd-soak/`.

4. **Restore-on-exit**: any synthetic Δpwm write MUST be paired with
   a deferred restore back to the controller-managed baseline, mirroring
   `polarity.WritePWM` + `defer restore` from the opportunistic prober
   (RULE-OPP-PROBE-10). A panic, ctx-cancel, or signal interruption
   leaves the channel at the last committed PWM byte; a deferred restore
   in `cmd/ventd-soak/excite.go` writes the baseline back.

5. **Single-shot mode never persists**: synthetic excitation runs are
   labeled in the observation log with a `EventFlag_SOAK_EXCITATION` bit
   (separate from `EventFlag_OPPORTUNISTIC_PROBE`) so consumers can
   filter them out of fleet-aggregate analyses. Excitation observations
   are admissible to Layer-B's RLS update but MUST NOT be admitted to
   Layer-C (RULE-CMB-OAT-01 already enforces the cross-channel quiet
   window; this rule extends the filter to label synthetic samples
   distinctly).

The v0.5.36 PR that ships the harness lands the read-only observer mode
+ this rule + a stub `excite` subcommand that prints "DEFERRED — synthetic
excitation driver requires daemon HTTP API integration; landing in a
v0.7+ follow-up" and exits 2. The full excitation driver lands in a
separate v0.7+ PR after the read-only observer has been used to confirm
that the daemon's existing opportunistic-probing path is producing
sufficient excitation on real hardware. If opportunistic probing is
sufficient, the excitation driver may not be needed at all.

A regression that:

- Drops `--enable-soak-excitation` from `excite` — fails CI.
- Imports `cmd/ventd-soak/` from `cmd/ventd/` — fails CI (module-graph check).
- Removes the deferred restore from a future excitation-driver impl —
  fails the bound subtest.
- Defaults the gate to true — fails the bound subtest.

This rule is documentation-only (single-h1, per the
`.claude/rules/RULE-STATE-*.md` pattern). It has no bound subtest because
the v0.5.36 PR ships only the read-only observer + the `excite` stub;
the bound subtest lands in the v0.7+ PR that implements the excitation
driver. Until then the rule is enforced by the structural gates above
(separate cmd, no production install) plus the stub's hard-coded
"DEFERRED" message.

The architectural lens for the entire v0.5.x → v0.6.0 cycle lives at
`docs/research/r-bundle/smart-mode-handoff.md`; the Phase C ship plan
at `/root/.claude/plans/you-are-a-30-vivid-pascal.md`.
