# spec-pro-v1 — ventd Pro v1 (fleet controller)

**Status:** draft, target Pro v1.0 launch ≥3 months after daemon v1.0 GA.
**License:** closed-source, commercial. Separate repository, separate build pipeline, separate release cadence from the GPL daemon.
**Companion docs:** `Monetisation_Strategy_for_ventd__Open-Source_Linux_Fan_Controller_Analysis.md` (strategy), `ventd_Pro_Feature_Selection__Homelab_and_NAS_Monetisation_Strategy_Analysis.md` (feature research).
**Predecessor specs:** spec-12 (web UI), spec-13 (verification workflow), spec-smart-mode (daemon control architecture).

---

## 1 · Why a separate product

ventd Pro is a **fleet controller**, not a fork of the daemon and not a sidecar inside it. Architecture choice is #3 from the three options reviewed: separate repo, separate binary, talks to one or more `ventd` daemons over the existing authenticated HTTP API.

Reasoning (ranked):

1. **GPL-3.0 boundary is unambiguous.** Daemon stays pure GPL-3.0. Pro is a different product communicating over a documented API with a vendored OpenAPI/proto schema. No "is this linking?" risk.
2. **Architecture matches the product.** 6 of 7 Pro v1 features (alerting, multi-host UI, retention, mobile/PWA, acoustic scheduling, support) are inherently fleet-controller features. Only one feature — drive-temp-aware curve engine — is daemon work, and that lands as **free daemon capability** while the curve-editor UI / preset packs / mix builder live in Pro.
3. **Single-host users still work.** Pro can run on the same box as a single daemon. "Fleet of 1" is a supported deployment.
4. **Proven playbook.** Plausible (CE + Cloud), Grafana (Agent + Cloud), Portainer (Agent + Server), TrueCommand (TrueNAS + TrueCommand). All are model #3.
5. **Solo-dev release cadence.** Daemon bugs don't block Pro; Pro features don't block daemon releases. Independent test surfaces.

**Repo layout:**

```
github.com/PhoenixDnB/ventd          GPL-3.0, daemon — unchanged
github.com/PhoenixDnB/ventd-pro      private, commercial — fleet controller
github.com/PhoenixDnB/ventd-pro-docs public — install/config docs only
```

The daemon never imports Pro code. Pro imports nothing from the daemon except a vendored copy of the public OpenAPI schema. Both communicate over HTTPS with the daemon's existing auth-token system (CoolerControl ships this in OSS now; ventd must match before Pro launch).

---

## 2 · Daemon-side prerequisites (must ship before Pro v1)

Pro v1 has a hard dependency on these daemon features. They are **free, GPL, in the daemon repo**, scheduled before Pro v1 begins.

| Prereq | Spec home | Daemon version | Why Pro needs it |
|--------|-----------|----------------|------------------|
| Authenticated HTTP API with token-bearer auth + TLS | new spec (or extension of existing web spec) | daemon v0.9.x | Pro talks to N daemons over the network; auth is non-negotiable |
| Stable OpenAPI/proto schema for read + control endpoints | extension of web/control specs | daemon v1.0 | Pro vendors this; schema breaks = Pro breaks |
| Drive-temp ingestion (smartctl / nvme-cli) as curve sources | new daemon spec | daemon v0.9 or v1.0 | Curve engine must accept drive temps before Pro's curve editor can target them |
| Multi-controller per fan (max-of-N composition) | new daemon spec | daemon v0.9 or v1.0 | Argus's killer feature; daemon-level capability |
| Hysteresis + rate-limit + temp-averaging as first-class curve primitives | extension of smart-mode | daemon v0.9 or v1.0 | Anti-oscillation must work in the daemon, not the UI |
| Curve-set switching primitive (named profiles + switch op) | new daemon spec | daemon v0.9 | Pro schedules switches; daemon executes |
| Stable event stream (fan stuck, fan failed, sensor over-temp, SMART pending-sector delta, NVMe critical-warning) | new daemon spec | daemon v0.9 | Pro's alert engine consumes this stream |
| 7-day on-host metrics retention (free baseline) | new daemon spec | daemon v0.9 | Pro extends to 30 days; daemon ships the 7-day floor for OSS users |

**If any prereq slips past daemon v1.0, Pro v1 slips with it.** The free daemon must be solid and feature-complete before Pro is sold; otherwise Pro is "buggy free thing wrapped in a subscription."

---

## 3 · Pro v1 feature list

Seven features ship in Pro v1, ranked by build order. Total estimate **~14.5 engineering weeks** for a solo dev assuming a working daemon and a clean Pro repo.

### 3.1 Curve editor + preset packs (the moat)

The drive-temp curve engine, multi-controller compositor, and hysteresis primitives all live in the **free daemon**. What Pro sells:

- **Visual curve editor**: drag-and-drop curve points, live preview against current sensors, multi-source mix builder ("max(CPU, GPU, hottest_drive)"), per-fan controller stack with weighting and override priority.
- **Hysteresis / rate-limit / temp-average controls** exposed as form fields rather than YAML.
- **Curated chassis-aware preset packs**: Supermicro X10/X11, Dell R-series IPMI, common Unraid mobo set, common TrueNAS Mini variants, ASRock Rack, MSI MEG, ASUS ROG. Each preset is a curated curve set + multi-controller composition + hysteresis defaults, signed off against the hardware DB.
- **Acoustic mode and scheduled curve switching**: time-of-day rules ("quiet hours 22:00-07:00 use Silent preset"), scrub-aware switching ("ZFS scrub running → use Performance preset for duration").

Build cost: **4 weeks**. Curve UI dominates; preset packs are content work that Phoenix's HW DB already feeds.

Moat: **Medium-high.** Capability is free; UX polish + curated content are paid. This is the canonical open-core line. Curated presets are content, content is hard to fork because forks don't have the test fleet behind them.

Fork risk: **Low.** Argus, Plex, Sidekiq all draw this exact line without spawning hostile forks.

### 3.2 Alerting engine + default rule pack + mobile push

Rule-based alert engine consuming the daemon event stream. Sinks: email (SMTP), Discord webhook, generic webhook, ntfy. Mobile push is via ntfy (BYO topic) or via a lightweight Pro-hosted push relay (defer the relay to v1.1 — see §6).

**Default rule pack ships pre-configured:**

- Fan stuck (RPM = 0 with PWM > 0 for >30s)
- Fan failed to start (PWM raised above kickstart, RPM still zero after kickstart window)
- Sensor over-temperature (configurable threshold per sensor class; sensible defaults)
- Drive SMART pending-sector delta increased
- NVMe `critical_warning` bit set
- ZFS scrub failure or pool DEGRADED (TrueNAS/Proxmox only — daemon needs ZFS hooks; defer to v1.1 if not ready)
- Daemon offline (Pro hasn't heard from a registered daemon for >2× heartbeat interval)

**Suppression**: scrub-aware (don't page on expected high temps during a scrub), acoustic-mode-aware (don't page on intentional quiet-hours throttling).

Build cost: **2 weeks** (rule engine + 4 sinks + templating + suppression logic). Reuse `containrrr/shoutrrr` for transport.

Moat: **Low** standalone (anyone can wire ntfy). **Medium** when bundled with the default rule pack and ventd-specific event types.

Fork risk: **Low.** Alerting is the #1 paywall pattern across 8 of 9 reference open-core products surveyed.

### 3.3 Multi-host fleet view (≤5 hosts)

Single web UI showing N daemons. Each daemon registers to Pro on startup with a Pro-issued token; Pro polls or subscribes to the daemon event stream over the network.

Pro v1 ships:

- Host list with online/offline status, version, last-heartbeat
- Aggregate dashboard (all fans across all hosts, sortable / filterable)
- Per-host drill-down (reuses the daemon's own UI primitives in iframe-or-equivalent — preferred is to render natively in Pro using the daemon API directly, no iframes)
- **Manual config push**: select a curve set in Pro, push to one or more hosts as a single user-driven transaction, with rollback on validation failure
- **No autonomous coordination between hosts.** Hosts are passive endpoints reporting status; all cross-host actions are user-initiated.

Build cost: **3 weeks**. Bulk: agent registration, host list, aggregate dashboard, config push transaction.

Moat: **Medium.** mDNS discovery + auth + cross-host config push is genuinely engineering work. CoolerControl is single-host as of 2026.

Fork risk: **Low** (this is the canonical open-core line; TrueCommand, Portainer, Netdata all do it). 

**Red-flag check:** This is *fleet management*, not agent swarm. Hosts are passive. Config push is user-initiated. No autonomous "agents negotiating curves." The architecture is explicitly designed to stay on the safe side of the line that cost $600 in the Cowork experiment.

### 3.4 30-day on-host historical retention + charts + CSV export

Pro stores its own 30-day rolling window of fan RPM, sensor temps, control values, drive temps, and event log. SQLite or Parquet on the Pro host's filesystem; downsampled past 24h to keep size sane. Basic chart UI in the Pro web UI; CSV export for any displayed view.

The daemon ships its own free 7-day window for OSS users; Pro extends to 30 days by aggregating daemon data into the central store and downsampling.

Build cost: **2 weeks**. Reuse SQLite + a small ring-buffer downsampler. Charts can use Chart.js or similar.

Moat: **Low-medium.** Easy to self-build, but few homelabbers do. Local-only storage (no cloud) avoids the "vendor takes my data" objection.

Fork risk: **Very low.** Retention paywalls are universally accepted (Grafana 14d → 13mo, TrueNAS Connect Plus 90d).

**Stress-test note:** The strategy doc said ">7 days." Spec specifies **30 days local on-host with no remote storage required**. Prometheus exporter for long-term retention stays in Team tier.

### 3.5 PWA mobile-friendly UI + push-notification glue

Existing Pro web UI gains a PWA manifest, responsive CSS, and push-notification subscription wired to the alerting engine from §3.2. Sold as a bundle with §3.2: install on phone → get alerts → fix from phone.

Build cost: **2 weeks** (responsive CSS + manifest + service-worker-based push).

Moat: **Low** as raw tech. **Medium** when bundled with alerting.

Fork risk: **Low-medium.** A community fork could polish a mobile UI, but they'd also have to build the alerting backend to make it useful. Bundle defends the moat.

### 3.6 Acoustic mode / quiet-hours scheduling + curated chassis presets

Already covered in §3.1. Listed here as a separate ship-line because it's a distinct user-facing feature with its own marketing claim.

Build cost: **1.5 weeks** beyond §3.1 (cron primitive + curve-set switching primitive in daemon + curated preset library content).

Moat: **Medium-high.** Curated presets are content, validated against Phoenix's HW DB.

Fork risk: **Low.**

### 3.7 Priority support

Operational only — no engineering work. Pro customers get a dedicated email channel with target response within 1 business day. Best-effort, no SLA. Team and Enterprise tiers will add SLAs later.

Build cost: **0 weeks.**

Moat: **High** — cannot be forked.

Fork risk: **Zero.**

---

## 4 · What's NOT in Pro v1 (deferred to v2 or Team/Enterprise)

| Feature | Reason deferred | Target |
|---------|----------------|--------|
| RBAC + OIDC SSO | Team-tier paywall pattern; Pro is single-admin | Team v1, ~3 wks |
| Prometheus exporter + remote-write | Team-tier; Pro keeps retention local | Team v1, ~1 wk |
| Daily config backup with cloud target | Team-tier; Pro keeps backup local | Team v1, ~2 wks |
| AIO pump curve as first-class target | Requires AIO test rig Phoenix doesn't own; **needs test partner** | Pro v2 |
| Trigger / state-machine logic ("if sensor X AND fan Y, switch curve") | Builds on multi-controller foundation; nice-to-have | Pro v2 |
| Anomaly-detection alerts (rolling z-score on RPM/temp) | Cheap (~1 wk) but feels speculative without v1 production data | Pro v2 |
| GPU + chipset coordinated curves | NVIDIA NVML + AMD `amdgpu` + Intel coverage; **needs test partner** for AMD/Intel breadth | Pro v2 |
| HA / hot-standby Pro controller | Premature for typical homelab fleet sizes | Pro v2+ |
| Cloudflare Tunnel / Tailscale Funnel quickstart wizard | Reuses what users already run; docs not code | Pro v2 (docs only) |
| Hosted "ventd Connect" tunnel | Phoenix doesn't run server infra; defer until ARR justifies it | Pro v3 or later |
| Multi-tenant org separation | Speculative until first MSP customer | Enterprise, 6-8 wks |
| Audit logs (immutable, exportable) | Enterprise-tier compliance | Enterprise, 2 wks |
| GPL-relief / OEM redistribution licence | Per-deal SKU, no engineering | Enterprise, 0 wks |
| 24×7 SLA support | Operational scaling concern | Enterprise add-on |
| Custom-branded UI / white-label | MSP-only requirement | Enterprise, 2 wks |
| ML / "AI" features framed as autonomous agents | Red-flag pattern; rejected | Out of scope permanently |
| RGB integration | Argus added late and only with user-supplied test rigs; not a homelab/NAS priority | Out of scope |
| Predictive bearing-failure ML | High build cost, unclear WTP, requires production telemetry that doesn't exist yet | Pro v3+ |

---

## 5 · Architecture

### 5.1 Process model

```
┌─────────────────────────────────────────────────────────┐
│ ventd-pro (closed source, commercial license)           │
│                                                         │
│  ┌────────────┐  ┌──────────┐  ┌────────────────┐      │
│  │ Web/PWA UI │  │ Alerter  │  │ Retention SQL  │      │
│  └────────────┘  └──────────┘  └────────────────┘      │
│         │             │                 │              │
│         └─────────────┴─────────────────┘              │
│                       │                                 │
│              Pro internal API                           │
│                       │                                 │
│  ┌────────────────────────────────────────────┐         │
│  │ Daemon-API client (vendored OpenAPI schema)│         │
│  └────────────────────────────────────────────┘         │
└─────────────────────┬───────────────────────────────────┘
                      │ HTTPS + bearer token
       ┌──────────────┼──────────────┐
       │              │              │
   ┌───▼──┐       ┌───▼──┐       ┌───▼──┐
   │ ventd│       │ ventd│       │ ventd│   (GPL-3.0, unchanged)
   │ host1│       │ host2│       │ host3│
   └──────┘       └──────┘       └──────┘
```

- Pro and daemon are **separate processes**, can run on the same host or different hosts.
- Communication is one-way request/response (Pro → daemon) plus daemon-initiated event stream (daemon → Pro via long-poll or Server-Sent Events; defer WebSocket to v2 unless v1 testing forces it).
- Daemons authenticate Pro via a daemon-issued token (registered in the daemon's existing auth system). Pro authenticates daemons via a Pro-issued registration token used at first contact.
- No daemon ever imports Pro code or links Pro libraries. The license boundary is process-level.

### 5.2 Storage

- **Pro state**: SQLite at `/var/lib/ventd-pro/state.db` (configurable). Holds host registry, alert rules, curve presets, retention data.
- **Pro config**: `/etc/ventd-pro/config.yaml` (license token, web bind address, TLS cert paths).
- **Pro license**: signed token in `/etc/ventd-pro/license.jwt`, validated locally; no phone-home (see §7).
- **Daemons**: continue to use whatever they already use; Pro never writes to daemon state directly.

### 5.3 Deployment

Three supported install modes:

1. **Single-host bundle**: Pro and one daemon on the same machine (TrueNAS app, Unraid plugin, Proxmox helper script). The "fleet of 1" case.
2. **Pro on management host + N daemons elsewhere**: typical homelab with separate management box (e.g., a NUC running Pro, daemons on each NAS / hypervisor).
3. **Single-binary Docker** for users running everything in containers.

Distribution:

- **TrueNAS app** (catalog submission gated on daemon catalog acceptance; Pro is a separate listing)
- **Unraid Community Apps plugin**
- **Proxmox helper script** (community-maintained or first-party)
- **Docker image** on Docker Hub + GHCR (free pull, license-keyed at runtime)
- **Static binary** (`.deb`, `.rpm`, `.tar.gz`) for direct install

---

## 6 · Tier definitions (v1 launch)

| Tier | Hosts | Price | Includes |
|------|-------|-------|----------|
| **Pro** | up to 5 | $6/mo or $72/yr | All features in §3 |
| **Team** | up to 25 | $29/mo or $290/yr | Pro + RBAC/SSO + Prometheus exporter + cloud config backup |
| **MSP/Enterprise** | unlimited | from $500/yr per-deal | Team + multi-tenant + audit logs + GPL-relief licence + 9×5 NBD support; 24×7 add-on |

Pricing anchored on:

- **$72/yr Pro** = midpoint of Plex Pass $69.99/yr and Tailscale Standard $72/yr (= $6/mo). Above Argus Monitor's $12/yr (justified by multi-host + NAS-specific) and below Nabu Casa's $78/yr.
- **$290/yr Team** = Portainer $199/yr/node halved-and-bundled for non-Kubernetes scope.
- **MSP from $500/yr** = Sidekiq Enterprise $269/mo floor implies B2B credibility floor; ventd's solo-dev positioning trims to $500/yr per-deal entry.

Free tier: the GPL daemon. No "free Pro tier with limits" in v1 — the daemon is the free product. Plausible's stance ("no free tier on the paid product, only a 30-day trial") is the model.

**Trial:** 30 days, full feature set, no credit card. License token issued by a static endpoint (no live SaaS billing for v1; manual order processing is fine for the first 50-100 customers per Sidekiq's playbook).

---

## 7 · Licensing implementation

- **License token**: signed JWT issued by Phoenix's keypair, embedded `host_count`, `tier`, `expiry`, `customer_id`.
- **Validation**: local-only (no phone-home). Pro fails closed if license expired by >30 days; warns in UI if expired by ≤30 days; warns in UI if `host_count` exceeded.
- **Renewal**: customer receives a new JWT by email on payment; replaces `/etc/ventd-pro/license.jwt`.
- **No DRM beyond JWT.** Closed-source binary + signed license is sufficient for the homelab market. Aggressive DRM in this segment is a fork-trigger.

For OEM dual-licensing of the daemon (separate revenue line in Enterprise), see the Monetisation Strategy doc §8 and the existing CLA work that must precede any external daemon PRs.

---

## 8 · API surface (Pro → daemon)

Pro consumes the daemon's existing public API. Schema vendored from the daemon repo's OpenAPI definition. Endpoints used by Pro v1:

| Endpoint | Purpose | Frequency |
|----------|---------|-----------|
| `GET /api/v1/health` | daemon liveness | every 30s |
| `GET /api/v1/devices` | fan + sensor + drive inventory | on registration + manual refresh |
| `GET /api/v1/metrics?since=…` | recent telemetry | every 60s |
| `GET /api/v1/events` (SSE) | event stream | persistent |
| `GET /api/v1/curves` | active curve config | on registration + manual refresh |
| `PUT /api/v1/curves` | push curve config | on user-initiated config push |
| `POST /api/v1/curves/validate` | dry-run validation | before push |
| `GET /api/v1/profiles` | list curve profiles | on registration |
| `POST /api/v1/profiles/{name}/activate` | switch profile | for scheduled switches |

**Pro does not call any non-public daemon endpoints.** Schema breaks in the daemon = Pro CI fails. Daemon must commit to API stability for at least one major-version cycle.

---

## 9 · Out of scope

- Modifying the daemon. Pro is implemented entirely in `ventd-pro`. Daemon prerequisites (§2) are tracked as separate daemon specs, not as part of this spec.
- Hosted SaaS. Pro v1 is self-hosted only. A managed cloud version is post-Pro v1.x, after ARR justifies running infrastructure.
- Mobile native apps. PWA is the v1 mobile story.
- Windows daemons. Linux-first through Pro v1, matching daemon roadmap.
- ML features framed as autonomous agents. Permanently out of scope per the red-flag rules.
- RGB. Permanently out of scope.

---

## 10 · Failure modes

| Failure | Impact | Mitigation |
|---------|--------|------------|
| Daemon API breaks between versions | Pro stops working against newer daemons | Vendor schema; daemon commits to API stability; Pro CI runs against multiple daemon versions |
| Pro requires daemon prereqs that slip past v1.0 | Pro v1 launch slips | Track prereqs as hard daemon-roadmap blockers (§2); don't start Pro v1 build until daemon prereq specs are merged |
| License JWT validation breaks (clock skew, signature error) | Customer locked out | Fail-soft for ≤30 days past expiry; clear error message; documented manual recovery path |
| Customer fleet > licensed `host_count` | Pro silently over-permits or blocks | Warn in UI when exceeded, soft-block new registrations, never disable existing alerting (don't punish paying customers for upgrade-path moments) |
| Community fork of Pro UI emerges | Erodes Pro adoption | Bundle alerting + UI + retention so the fork has to replicate three things, not one; keep curated preset packs as content moat |
| Daemon's own auth / TLS implementation has CVE | Pro inherits the vulnerability | Pro's setup wizard validates daemon version is patched before registering |
| Pro v1 launches before daemon v1.0 is solid | "Buggy free thing wrapped in a subscription" reputation damage | Hard rule: Pro v1 launch ≥3 months after daemon v1.0 GA, ≥150 boards in HW DB, ≥3000 GitHub stars |
| Trial-to-paid conversion is low | Revenue doesn't materialize | Plausible's pattern: no free tier on the paid product, 30-day full-feature trial only; if conversion is <5% after 6 months, revisit pricing not features |
| MSP customer asks for multi-tenant before it's built | Lost deal or premature build | Defer politely; Sidekiq's stance ("we'll build it when 5 customers ask") |

---

## 11 · Definition of done — Pro v1 as a whole

Pro v1 ships when **all** are true:

1. Daemon prereqs in §2 are merged on `main` and tagged in a daemon release ≥v1.0.
2. All seven features in §3 are implemented, tested, and documented.
3. Single-host install path works on TrueNAS SCALE, Unraid, Proxmox, and bare Debian/Ubuntu/Fedora.
4. Three-host install path works (Pro on management host + 3 daemons elsewhere).
5. License JWT validation handles all four states: valid, expired ≤30d, expired >30d, host_count exceeded.
6. Trial flow works end-to-end: customer requests trial → receives JWT by email → installs Pro → installs license → 30-day countdown visible in UI.
7. Documentation site (`ventd-pro-docs`) covers install, register-daemon, alert-rule examples, curve-editor walkthrough, troubleshooting.
8. At least one paying customer not personally known to Phoenix has installed and used Pro for ≥7 days without breaking issues.
9. Pricing page is live, billing path is live (Stripe or LemonSqueezy; per Sidekiq's "subscription from day 1" rule).
10. Trademark "ventd" filed (USPTO TEAS Plus); CLA on the daemon repo; copyright clean for Pro repo.

---

## 12 · Stage gates before starting Pro v1 build

From the strategy doc, repeated here as preconditions:

- Daemon v1.0 shipped with predictive model and "world's first" launch story landed
- Hardware DB ≥150 boards across ≥10 vendors (currently 52 / 6)
- Listed in Unraid Community Apps and TrueNAS app catalog
- GitHub stars ≥3000
- CLA in place; trademark filed; brand for the paid product chosen (could be "ventd Pro" or a separate brand — strategy doc leaves this open; recommend deciding before build starts so docs/marketing aren't reworked)
- Closed-source web UI prototype Phoenix has personally used for ≥30 days

If any stage gate isn't met, Pro v1 build hasn't started.

---

## 13 · Why not ship features faster / cheaper

The temptation is to ship a smaller Pro v1 (just curve editor + alerting) earlier. Three reasons not to:

1. **WTP signal**: $72/yr requires the bundle. A single feature at $72/yr looks overpriced; the seven-feature bundle benchmarks correctly against Plex Pass / Tailscale Standard / Netdata Homelab.
2. **Moat depth**: any single feature is forkable in 2-4 weeks. The bundle's defensibility comes from breadth — alerting + UI + retention + presets together are 14+ weeks of fork work.
3. **Customer expectations**: paying customers compare Pro to TrueCommand, Portainer Business, Nabu Casa. Each ships ≥5 substantive features at this price band. A 2-feature Pro looks underbaked.

The right cost-cutting move is to defer Team-tier features (RBAC, Prometheus, cloud backup) to a Team v1.0 release ~2 months after Pro v1, not to thin out Pro v1 itself.
