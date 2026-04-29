# Next chat — smart-mode architecture for ventd v0.5.x → v0.6.0

## What just happened (the pivot)

Previous chat started as a spec-10 doctor rewrite (read-only → fix-capable).
Mid-conversation I (Phoenix) reframed the entire roadmap. The core
realisation:

**v0.3 → v0.5 built the substrate. Now I want to make it smart.**

Everything ventd has shipped so far is fan-control plumbing — HAL backends,
calibration store, hardware catalog, install contract, web UI scaffolding,
predictive thermal *spec shell*. None of it is smart. The catalog is the
source of truth, the controller is reactive, calibration is one-shot at
install. The masterplan north star always said
"self-calibrating, learning, hot-plug aware, predictive, works on every
fan" — the phased roadmap deferred all of that.

I'm un-deferring it. v0.6.0 won't be tagged until smart-mode is real.

## The constraint that drives everything

**"ventd must work for the very first user no matter who they are."**

I have no users yet. I won't have a community feedback loop. I cannot ship
a "supported hardware list" and tell people their board isn't on it.
Whoever installs ventd first — TrueNAS Scale admin, NixOS enthusiast,
Framework laptop owner, custom DIY desktop builder — it must work.
Flawlessly.

This rules out:
- Seed lists ("we'll grow this with community reports")
- Tier-3 fallback as graceful degradation (must be primary, must be tested)
- "Phased rollout to known cohort" (no cohort exists)
- "Wait and see what users hit" (no users)

This requires:
- Behavioural detection over enumeration (BIOS override detected by
  probing response, not by board match)
- Catalog-less mode as the primary code path (catalog is a fast-path
  overlay)
- Online thermal model with cold-start priors (useful in minutes, not
  weeks)
- Confidence-gated control (predictive when confidence > threshold,
  reactive fallback when not, UI shows which mode + why)
- Continuous calibration (replaces one-shot)
- Doctor as runtime conscience, not install-time triage (spec-10
  reframed)

## Fork D — the chosen path

I rejected three alternatives:
- Fork A (ship v0.6.0 as currently planned, doctor + UI + experimental):
  dishonest about the product promise.
- Fork B (3-6 months of design, no shipping): kills momentum, ADHD risk.
- Fork C (ship v0.6.0 polish, then v0.7.0 anyone-hardware): wastes
  $87-150 polishing the wrong substrate.

**Fork D — incremental smart-mode patches as v0.5.x, no v0.6.0 tag until
smart-mode is complete.**

Same destination as Fork B. Same throw-away/keep ratio. Preserves the
shipping cadence and cost discipline that have been working. Each step
ships, gets HIL-tested, validates the architecture before the next step
depends on it.

## What we throw away vs keep

**Throw away (or major rework) when smart-mode lands:**
- spec-12 mockups — show static current values, no confidence/learning/
  warming UI. PR 4 setup flow assumes catalog-hit path. ~60-70% rework
  pending.
- spec-10 as I just rewrote it — issue-enumeration model is wrong for
  smart-mode. Reframe entirely as runtime conscience.
- spec-04 PI autotune — subsumed by predictive controller. Don't ship
  separately.
- bios_known_bad.go (was about to seed it) — replaced by behavioural
  detection.
- Tier-3 fallback semantics in spec-03 — inverted. Fallback becomes
  primary; catalog becomes optimisation.

**Keep:**
- HAL backend registry — universal, no rewrite.
- Calibration store schema — works; what gets stored changes (continuous
  samples not one-shot).
- Hardware catalog — demoted from prerequisite to fast-path overlay.
- Install contract, AppArmor, polkit — orthogonal.
- spec-15 experimental framework — orthogonal, ships either way.
- Web UI token system / sidebar / shell — design system survives, page
  contents change.
- Diag bundle infrastructure — orthogonal.
- spec-05 predictive thermal research — graduates from "v1.0 spec shell"
  to "v0.6.0 implementation."

## Phoenix infra (testing reality)

- Win11 desktop (13900K + RTX 4090) in WSL2 — NOT a Linux HIL, but I'll
  reinstall it native Linux if needed for testing
- Proxmox host 192.168.7.10 (5800X + RTX 3060) — primary VM infra
- MiniPC 192.168.7.222 (Celeron) — low-end Linux HIL
- **3 laptops** that I can install any OS to (Phoenix added this in pivot
  chat — significant for laptop EC behavioural testing)
- Synthetic tests where HIL impossible

I will reinstall whatever I need to test smart-mode behavior. The HIL
fleet is bigger than the original masterplan assumed.

## What I want from the next chat

**Smart-mode architecture work.** Not a spec yet. We need to design the
shape of smart-mode before any spec gets written. Topics, in rough
priority order:

1. **Catalog-less mode as primary path.** What does the probe layer need
   to expose? What's the auto-discovery contract for `/sys/class/hwmon`
   + DMI + runtime probing? How does it produce a working fan map without
   any prior catalog knowledge? How does it handle ghost hwmon entries,
   tach-less fans, fixed-speed fans, broken sensors? What's the safety
   envelope during the unknown-hardware probe?

2. **Behavioural BIOS-override detection.** Replace bios_known_bad.go.
   How does ventd detect Q-Fan / Smart Fan / SilentFan / Cool'n'Quiet /
   firmware-curve override by *behavior* (write a value, watch what
   happens, classify)? What's the probe sequence? What's the false-
   positive risk? How does it integrate with the existing
   bios_overridden flag in calibration records?

3. **Online thermal model + cold-start priors.** spec-05 currently
   assumes weeks of accumulated history before predictive mode is
   useful. What does cold-start look like? Per-CPU-family priors? Per-
   chassis-class priors? How quickly can the model produce useful
   predictions on fresh install? What's the confidence metric, and when
   does ventd switch from reactive to predictive?

4. **Confidence-gated control loop.** What's the controller architecture
   that runs reactive *and* predictive simultaneously and switches
   between them based on confidence? How is confidence reported to the
   user? What does the UI look like (predictive mode active / warming up
   / reactive fallback because of X)?

5. **Continuous calibration.** Replace one-shot calibration. How does
   ventd opportunistically sample fan response during normal operation
   without disrupting the user? What's the sample-rate, the
   accumulation policy, the staleness model?

6. **Doctor as runtime conscience (spec-10 reframe).** Under smart-
   mode, what does doctor *do*? It's not detecting catalog mismatches
   (catalog is optional). It's not enumerating known issues
   (enumeration is wrong model). It's verifying the smart-mode runtime
   is healthy — confidence is converging, calibration is fresh, no
   sensor anomalies, no controller divergence. Different shape entirely.

7. **v0.5.x patch sequence.** Order these into shippable patches. Each
   patch is HIL-tested on Phoenix's fleet before the next depends on
   it. Estimate token costs (chat + CC) per patch.

## How we work (rules of engagement for the next chat)

The next-chat Claude needs to know:

- **I'm Phoenix, solo dev, GitHub PhoenixDnB, ventd repo at
  github.com/ventd/ventd.** I have ADHD; keep me on task; warn when
  conversations have gone too long.
- **Output discipline:** direct, factual, no fluff, no preamble, no
  emojis, no validation language. Token-frugal. Short answers for short
  questions. Long-form only when the task requires it.
- **Design here, implement in Claude Code.** This chat produces
  artifacts (specs, prompts, patches, architecture docs). Not code
  walls.
- **Ask before assuming.** One targeted question beats three wrong
  paragraphs. I'll confirm before you write 400 lines of spec.
- **Research access:** flat-rate Max plan, use web search aggressively
  for current ecosystem questions. Always read project files I link
  before answering.
- **Cost awareness:** target $10-30 per spec execution in CC, $300/mo
  total. Surface cost before recommending anything expensive. If
  something feels like 20+ tool calls, surface and possibly recommend
  Research feature.
- **No red-flag tools:** parallel / swarm / hive / orchestrator / agent
  team / fleet / farm pattern-match the $600 weekend. Push back if I
  drift toward them.
- **Memory awareness:** my Claude memory has 30 entries covering
  identity, infra, repo state, lessons learned, and the smart-mode
  pivot. Reference it naturally.

## First question for next-chat Claude

**Don't start writing architecture immediately.** Ask me clarifying
questions about the smart-mode constraints first. In particular:

- What does "works flawlessly" mean for an unknown-hardware first user
  who has, e.g., a 2010 Sandy Bridge motherboard with no hwmon driver
  at all? Is "ventd refuses to run, tells you why, points you at
  contributing a profile" acceptable, or does even that violate the
  constraint?

- What's the safety envelope on first contact with truly unknown
  hardware? I.e. how aggressively can the probe layer write PWM values
  to learn the response curve before we've calibrated anything? What's
  the worst-case if we're wrong?

- Is there a hardware class I'm willing to *explicitly* not support?
  (e.g. "ventd does not support Apple Silicon Linux until Asahi
  exposes fan control", "ventd does not support BSD until v2.0", etc.)
  These are honest scope boundaries, different from "we don't have
  this in the catalog yet."

- How do I want to validate smart-mode is working? What's the success
  metric per v0.5.x patch? "Phoenix can install ventd on a freshly-
  imaged laptop and fans calibrate within 5 minutes without manual
  config" — that kind of thing. Need concrete validation criteria
  before we scope work.

After those answers, we can start scoping the v0.5.x patch sequence
and the architecture decisions inside each one.

## Files to load at the start of next chat

Project files (in /mnt/project/) that the next-chat Claude should read
before starting:

- `ventdmasterplan.md` — north star and phase structure (especially §1
  and the "what ventd explicitly does NOT do" section)
- `spec-05-predictive-thermal.md` — the v1.0 spec shell that's now
  graduating to v0.6.0 implementation
- `spec-03-profile-library.md` — catalog design (now demoted to fast-
  path overlay)
- `spec-03-amendment-pwm-controllability.md` — pwm controllability
  research (relevant to behavioural detection)
- `2026-04-hwmon-driver-controllability-map.md` — controllability data
  (relevant to behavioural detection)
- `PredictiveThermalControlResearch.md` — research backing for spec-05
- `hwmon-research.md` — hwmon-layer research notes
- `2026-04-hwmon-generic-catalog.md` — generic catalog (relevant to
  catalog-less mode)

Do not auto-load all of these. Read them as questions surface. Token
budget matters even on Max.

## What this chat is NOT producing

- A spec yet. Architecture first.
- Code. (Implementation always in Claude Code.)
- A doctor rewrite. (Doctor reframes after smart-mode lands; the spec-
  10 rewrite I just produced is shelved until then.)
- A v0.6.0 critical path. (That's downstream of architecture
  decisions.)

---

**End of handoff prompt.**
