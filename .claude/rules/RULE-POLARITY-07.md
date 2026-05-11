# RULE-POLARITY-07: IPMI polarity probe vendor-dispatch surface is reserved for v0.7+; v0.5.39 removed the unused implementation per #1071.

**v0.5.39 deletion**. The original v0.5.x design declared an
`IPMIVendorProbe` interface in `internal/polarity/ipmi.go` with three
concrete implementations (`SupermicroIPMIProbe`, `DellIPMIProbe`,
`HPEIPMIProbe`) covering Supermicro OEM, Dell firmware-locked, and
HPE profile-only channels. Pass-2 of the comprehensive code audit
(`docs/audits/2026-05-11/pass-2-interface-conformance.md`)
identified the surface as fully dead in production:

- 0 production sites took a parameter of type `IPMIVendorProbe`.
- 0 production sites stored a `IPMIVendorProbe` field.
- 0 production sites returned `IPMIVendorProbe`.
- 0 production sites type-asserted against `IPMIVendorProbe`.
- 0 production sites instantiated any of the three concrete vendor
  probes.

Every reference outside the declaration lived in the rule's bound
subtest, which constructed each vendor probe directly and verified
the per-vendor behaviour in isolation — but the rule's implicit
promise that the interface is **used** by the wizard / probe path
was not enforced. RULE-POLARITY-07 was documentation, not behaviour.

**v0.5.39 chose option 2** from the audit recommendation: delete the
dead surface. Phoenix's HIL fleet (`project_hil_fleet.md`) contains
no IPMI-controlled server (desktop + MiniPC + Proxmox + 2 laptops +
Steam Deck), and v0.6.0's smart-mode target is consumer
desktop/laptop first-user-experience. IPMI polarity probing is
genuinely deferred to v0.7+ when server hardware enters the HIL
fleet.

The control-path IPMI surface (`internal/hal/ipmi/`) is unaffected
by this deletion. RULE-IPMI-1..7 + RULE-WD-IPMI-ROUTING continue to
cover the BMC fan-control + watchdog restore paths; those are wired
via `cmd/ventd/ipmi_watchdog.go::registerIPMIWatchdogEntries`. What
v0.5.39 deleted was the **polarity-probe direction-detection**
vendor dispatch — a separate concern that the audit confirmed had
zero production callers.

A v0.7+ PR that re-introduces IPMI polarity probing (wired via the
wizard PhaseGate machinery from #800) will:

1. Re-add the `IPMIVendorProbe` interface + the concrete probe
   types in a new file under `internal/polarity/` (or in a fresh
   `internal/polarity/ipmi/` subpackage).
2. Wire a real construction site in the wizard's setup phase that
   detects IPMI hardware (DMI chassis_type 23, `/dev/ipmi*`
   presence) and dispatches the appropriate vendor probe.
3. Re-bind RULE-POLARITY-07 to a subtest that exercises the wizard
   call site, not the vendor probes in isolation (the
   helper-extraction pattern: rebind the rule against the
   production caller, not the helper). The audit's WEAK→SOLID
   recipe applies here just as it does for the v0.6.0 #1075
   smart-mode wiring helpers.
4. Either restore Supermicro / Dell / HPE coverage or scope to
   whichever vendor lands in HIL first; the rule file is the slot
   that future work amends.

The full implementation history (the deleted code + tests) is in
git at the parent of v0.5.39. Anyone re-introducing the surface
should consult `git log --diff-filter=D --name-only -- internal/polarity/ipmi.go`
to recover the v0.5.x baseline rather than starting from scratch.

This rule is documentation-only in v0.5.39+. It has no bound
subtest because the surface it would constrain no longer exists.
A regression that adds new IPMI polarity-probe code without
re-wiring it is caught at review time, not at test time — the
audit recipe (`tools/audit/ghost-code`) will surface the new
dispatch surface as GHOST on the next pass and the operator can
choose to wire-or-delete as before.
