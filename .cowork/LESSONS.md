...existing content preserved, appending lesson 9 at end...

---

## 2026-04-18T (clean-slate-resume, claude-opus-4-7) — ninth lesson (HIGH IMPACT)

**Inefficiency observed**: spent five consecutive `spawn_cc()` dispatches on a single deployment, each failing with one narrow permissions/directive error and each fixed by one narrow edit. Failure chain: EACCES on /tmp/cc-runner (ownership/mode) → EPERM chown (CAP_CHOWN) → EPERM chmod (CAP_FOWNER) → NNP blocks sudo → EPERM setgid (CAP_SETUID/SETGID/AUDIT_WRITE/DAC_READ_SEARCH) → EROFS on /tmp/tmux-986 (ProtectSystem=strict). Each symptom was isolated and patched; the underlying architectural incoherence — a service that crosses user boundaries under hardening directives that forbid crossing them — was not named until attempt five. By attempt two (CAP_CHOWN), the pattern was already visible: "this design approximates root via piecewise capability grants; collapse the users instead." I failed to name it then.

Root cause of the failure-to-name: (a) sunk-cost momentum after the first symptom-fix ("one more and it'll work"); (b) no explicit stop rule for consecutive same-class failures; (c) LESSONS.md protocol is retrospective — lessons are read once at session start, then I fail to match live symptoms against them during the session. Lesson #6 in this file is the exact pattern that was repeating, and I didn't cite it until after the fifth failure. Reading ≠ applying.

**Fix applied**: four concrete protocol changes, committed to this file so subsequent sessions inherit them.

(1) **Two-failure stop rule.** After two consecutive failures of the same class (perms / unit-directive / capability / network-config), halt symptom-chasing. Stop writing the next narrow fix. Instead: name the architectural assumption that's failing, enumerate 2–3 redesign options, present to user with `ask_user_input_v0`. Cost analysis: n+1 symptom fixes is O(n·user_wait). Redesign is O(1).

(2) **Pre-dispatch design audit.** Before dispatching CC against infra I authored in a prior session, spend exactly one turn auditing the design for incoherence against its runtime environment. Concrete checklist for services: (i) does the service cross user/namespace boundaries? If yes, what caps does each crossing require? (ii) does the unit's hardening set (NNP, ProtectSystem, PrivateTmp, ReadWritePaths) conflict with the child process's expected filesystem view? (iii) is there a simpler model where no crossing happens? One turn spent here saves n round-trips later. This is lesson #6 restated as a pre-flight check instead of a post-mortem.

(3) **In-session lesson citation.** When a failure matches a LESSONS.md pattern, the response must name the lesson by number ("this is lesson #6 class: infra shipped without end-to-end smoke") and state the specific application ("applying: propose user-collapse redesign"). If I can't cite the lesson number, I'm re-learning the lesson, not applying it. Missing citations are evidence the protocol is broken, not that the lesson wasn't relevant.

(4) **Attempt-count budget.** A single infra block gets 3 dispatch attempts maximum. At attempt 3, halt and offer architectural options to the user. Today I went to attempt 5. The marginal value of attempts 4 and 5 was negative (each deepened the capability pile before the inevitable pivot).

**Handoff reducible to MCP**: none this session. This is a Cowork protocol change, not a tool gap. The tool (spawn-mcp) is fine; my use of it was the problem.

**Secondary observation**: lesson #6's "ephemeral smoke target" rule was not applied because spawn-mcp is *itself* the ephemeral target infrastructure. Chicken-and-egg: the tool that would have caught these failures is the tool that has them. Resolution: until spawn-mcp is stable, use Incus containers on phoenix-desktop as the smoke target for spawn-mcp itself. Once stable, spawn-mcp smoke-tests future services.
