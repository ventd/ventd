# ventd-R20-fleet-federation.md

**ventd R20 — Federated Learning Across User-Owned Fleet (LAN-Internal State Sharing)**
*Research artifact, R-bundle round 20. Spec target: v0.8.0+ (post-smart-mode v0.6.0). Status: pre-spec design exploration, not implementation.*

---

## 0. Framing

R20 takes a deliberately narrow scope: **bilateral, peer-to-peer, LAN-internal sharing of locked smart-mode state (R1–R19) between hosts that the same human owns and configures.** It is *not* federated learning in the gradient-descent sense of McMahan et al. (FedAvg, arXiv:1602.05629); the controller does not perform SGD. It is closer to federated *state synchronization* of small, structured, slowly-changing tables — Layer A curves, Layer B ARX coefficients, optionally Layer C overlays — across a co-administered homelab fleet (TrueNAS, Unraid, Proxmox, MiniPCs).

The R-bundle so far has produced a tightly locked smart-mode design: Layer A curve table, Layer B per-channel ARX coupling coefficients via Recursive Least Squares (RLS) with a κ identifiability detector (R9, R17), Layer C per-(channel, signature) marginal-benefit overlay where signatures are SHA-256 hashes of `proc/comm` tuples and are explicitly privacy-bound (R7). State persists via the KV/Blob/Log primitives at `/var/lib/ventd/` shipped in spec-16 of v0.5.1. R20 must be **purely additive** to that locked shape; it cannot mutate Layer A/B/C semantics, only annotate them with provenance and merge-eligibility metadata, and add a small new namespace for peer-table state.

Spec-14 (curated, internet-scale, opt-in upload of profiles) is the public counterpart; R20 is its private, automatic, LAN-only sibling. The design must explicitly avoid the multi-agent "orchestrator/coordinator/swarm" pattern that has previously cost time and money — every aspect of R20 is symmetric peer-to-peer.

---

## 1. Threat Model and Trust Boundary

A **user-owned LAN fleet** is defined operationally as: *a set of hosts on which the same human possesses root, has installed `ventd` deliberately, and has executed a pairing action joining each host into a named fleet identity.* The fleet is **not** defined by L2/L3 topology alone — being on the same `/24` is necessary but not sufficient. Pairing is what establishes membership; mDNS only announces *candidacy* for pairing.

Trust assumptions (in decreasing strength):

1. **Co-administration.** All hosts are configured by one human; there is no adversarial multi-tenant scenario inside the fleet.
2. **Aligned interest.** All hosts want each other to fan-control well. There is no incentive for a peer to lie about its measurements *intentionally*.
3. **Bounded blast radius.** Compromise of one host (rooted by malware, or simply broken — e.g. a stuck `tach` sensor reporting 0 RPM at full PWM) must not poison the fleet. Trust is per-host and per-layer; a peer can be quarantined without re-pairing the whole fleet.
4. **Privacy ≥ same as standalone.** The smart-mode privacy bound from `spec-smart-mode.md` — that raw `proc/comm` SHA-256 hashes never leave a host without explicit opt-in — applies *unchanged* to R20. A user-owned fleet is still a network of *processes that may not all be the same person's processes* (kid's gaming PC, parent's work laptop, partner's NAS).

**Attacker model:**

- **L1 — Network-local passive.** Someone who can observe the LAN (rogue IoT device, guest Wi-Fi bleed). Mitigation: all payload traffic is authenticated-encrypted (Noise IK or mTLS); discovery (mDNS) is plaintext but reveals only `_ventd._tcp` SRV/TXT records, no fan data.
- **L2 — Network-local active.** ARP/DHCP/mDNS-spoofer on the LAN. Mitigation: pairing produces a long-term static public key per peer; post-pairing, all sessions cryptographically pin to that static key. mDNS spoofing degrades to denial-of-discovery, not impersonation.
- **L3 — Compromised peer.** A peer was rooted, or simply has a broken sensor. Mitigation: per-layer confidence weighting, hardware-fingerprint gating (§3), spec-16 KV versioning for rollback, explicit `peer.quarantine` flag (§10).
- **Out of scope (L4):** Internet-facing attackers. R20 listens only on link-local interfaces by default; AppArmor restricts the new daemon socket binding.

R20 is explicitly **not internet-facing**. WAN federation is spec-14's territory (curated, manual upload, no bilateral state sync).

---

## 2. Discovery Protocol

Three options were evaluated:

**(a) mDNS / DNS-SD (RFC 6762, RFC 6763).** Cheshire & Krochmal's Multicast DNS (RFC 6762, Feb 2013, rfc-editor.org/rfc/rfc6762.html) defines DNS-style queries on the link-local multicast addresses 224.0.0.251 / FF02::FB on UDP/5353 with IP TTL 255 (so traffic stays on the local link), and the `.local` namespace; RFC 6763 (rfc-editor.org/rfc/rfc6763.html) specifies how SRV+TXT records describe service instances. ventd would advertise `_ventd-fleet._tcp.local` with a TXT record containing `proto=1`, `pubkey-fp=<32hex>`, `hwfp=<sha256-prefix>`, `state-ver=<int>`. Pure-Go zeroconf libraries exist (e.g. `github.com/grandcat/zeroconf`, BSD-3) that compile CGO-free.

**(b) Static `peers.yaml`.** User hand-edits a YAML file listing peer IPs and pubkey fingerprints. Zero magic, brittle, violates the "zero-terminal" intent of ventd, but reliable on segmented VLANs that drop multicast.

**(c) Manual pairing token (magic-wormhole-style).** One host emits a one-time human-pronounceable code; the other consumes it. Excellent UX for the *first* pairing but unsuitable as steady-state discovery (every reboot would re-pair).

**Recommendation: (a) mDNS for discovery + (c) one-shot wormhole-style code for the *first* pairing exchange + (b) `peers.yaml` as a fallback override.** Discovery and authentication are decoupled: mDNS only tells you "host X claims to be a ventd fleet candidate"; the wormhole-style code (a short SPAKE2-equivalent OTP — the magic-wormhole protocol uses SPAKE2 with PGP wordlist codes per magic-wormhole.readthedocs.io) is what cryptographically binds the static keys on *both* hosts to each other on first contact. Subsequent contacts use the static keys, with mDNS only used to find the current IP. This is symmetric — there is no "controller" host. Every node is both a discoverer and a respondable. Critically, it dodges a central coordinator: no Tailscale-style coordination server (tailscale.com/blog/how-tailscale-works documents the hub-and-spoke control plane / mesh data plane split that R20 explicitly does not replicate, since ventd has no operator-run cloud).

---

## 3. Fingerprint-Match Protocol for Merge Eligibility

Spec-05 already defines a hardware fingerprint composed of DMI `sys_vendor` + `product_name` + BIOS version + the sorted set of hwmon driver module names. R20 reuses this verbatim and layers three equivalence classes on top:

**Tier 1 — EXACT (eligible for Layer A + B merge).** Same DMI sys_vendor, same DMI product_name, same BIOS major version, identical hwmon driver set, identical fan count, identical PWM range per channel (`pwmN_min`/`pwmN_max`), identical tach channel set. Rationale: only at this tier are the *physics* of the system the same — same VRM thermal mass, same fan curves at the firmware level, same sensor positions. RLS coefficients learned on host X are physically meaningful on host Y.

**Tier 2 — FAMILY (eligible for Layer A merge only).** Same DMI sys_vendor + product_name; BIOS *minor* differences allowed; hwmon driver set must be identical; fan count and PWM range must match. Rationale: BIOS minor revisions occasionally tweak fan-table defaults but rarely change sensor topology. Curve tables (Layer A) are robust to these tweaks because they are coarse static lookups; ARX coefficients (Layer B) are not, because they encode small-signal gains that BIOS fan-curve overrides can perturb.

**Tier 3 — REJECT.** Any sensor topology mismatch (different number of `tachN` inputs, different `tempN` sources, different hwmon driver set, different PWM range) = no merge of any layer. Rationale: physically different control plant; merging would actively harm the recipient.

The matching is computed locally by each peer on every received state bundle; the sender does not assert eligibility. The fingerprint and tier are persisted in the per-peer KV namespace as `peer/<id>/hwfp` and `peer/<id>/tier` so `ventd doctor` can explain merge decisions.

---

## 4. State-Merge Semantics Per Layer

The first principle: **R20 is federated state sync, not federated learning.** McMahan et al. (arXiv:1602.05629, "Communication-Efficient Learning of Deep Networks from Decentralized Data," v3 Feb 2017) introduce FederatedAveraging (FedAvg) as a server-coordinated weighted average of locally-trained model parameters; their setting is non-IID mobile-device data with a deep network. The applicability to ventd is partial: the *idea* of weighted averaging keyed by local sample count is directly useful; the *mechanism* (rounds of SGD coordinated by a central server) is not, because (a) ventd's models are not trained by SGD, (b) there is no central server in R20 by design, and (c) the state is small enough (kilobytes, not megabytes) that we can afford to ship full state rather than gradient deltas.

So: **gossip-style pairwise state exchange + per-layer merge function + monotonic version vectors**, drawing on FedAvg's weighting intuition without inheriting its architecture.

### Layer A — Curve table

Layer A is a per-channel piecewise-linear `(temp → PWM)` table. Merge strategy: **confidence-weighted average per knot, monotonic version stamp, family-tier eligible.**

For each `(channel, knot_temp)` cell `c_i` with local sample count `n_i` and converged duty `d_i`, the merged duty is `Σ(n_i · d_i) / Σ n_i` clamped to `[pwm_min, pwm_max]`. A monotonic `state_ver` is bumped on each merge; a peer that has already absorbed a given `state_ver` ignores duplicate gossip. This is FedAvg's weighting rule applied to a static table rather than to network weights.

### Layer B — Per-channel ARX coefficients via RLS

Layer B is the harder case. RLS state is a coefficient vector `θ` plus a covariance matrix `P`; naive averaging of two `θ` vectors is *not* equivalent to running RLS on the union of observations. The κ identifiability detector from R9 already exposes a per-channel scalar in `[0, 1]` indicating how confidently `θ` is constrained by the observed input excitation.

Recommendation: **only-merge-if-both-converged, with κ-gated confidence weighting at exact-tier eligibility.**

Concretely:

- If either peer's κ for the channel is below the smart-mode "converged" threshold (per R17), do not merge — the under-excited peer adopts the better-excited peer's `θ` wholesale (a "warm-start" semantic) but resets its own `P` to the post-merge prior covariance, not the incoming `P`. This avoids the "two unconverged guesses average to a worse guess" failure mode.
- If both κ are above threshold, weighted average `θ_merged = (κ_A · θ_A + κ_B · θ_B) / (κ_A + κ_B)`, and `P_merged` is set to a conservative diagonal prior (the harmonic mean of trace(P_A) and trace(P_B) on the diagonal). Diagonal-only because off-diagonal averaging of non-PSD-preserving combinations risks producing a `P` that is no longer positive semi-definite, breaking RLS stability.
- Reject merge entirely if hwfp tier ≠ EXACT.

### Layer C — Per-(channel, signature) overlay

**Default: Layer C is NOT shared between peers, ever, even within a fleet.** The rationale is the privacy threat model: a `proc/comm` signature is an opaque hash, but the *set* of signatures present on a host is itself a fingerprint of running software. A homelab where parent's work laptop and kid's gaming PC are both `ventd`-managed should not result in the parent's laptop learning that the gaming PC has Steam-related signatures, even via opaque hash.

If the user explicitly opts in (per-host flag `fleet.share_layer_c=true`), Layer C may be merged at exact-tier only, with the same confidence-weighted scheme as Layer A but keyed by `(channel, sig)`. New signatures from a peer are accepted only if the local host already has a Layer A entry for that channel (a sanity check that the topology matches at minimum).

---

## 5. Conflict Resolution

Two hosts of the same hardware learn divergent Layer B coefficients. Three styles of resolution were considered:

- **Last-write-wins (LWW) register** (Shapiro, Preguiça, Baquero, Zawirski, "Conflict-free Replicated Data Types," INRIA RR-7687, 2011, inria.hal.science/inria-00609399v2/document) — simple, but throws away all information from the loser. Wrong for Layer B because the "loser" may have been the better-excited host.
- **Confidence-weighted blend** — keeps both signals proportional to their RLS κ (and Layer A sample counts). This is the FedAvg-flavoured rule above.
- **Recency tiebreaker** — when confidences are within an epsilon band, prefer the more recent observation. Hardware drifts (dust, thermal paste degradation) so freshness has weak prior value.

**CRDT applicability.** Shapiro et al.'s framework distinguishes state-based (CvRDT) and op-based (CmRDT) replicas, with strong eventual consistency guaranteed when merges are commutative, associative, and idempotent. ventd's natural primitives map as follows:

- **Layer A knot table → state-based PN-Counter-style CvRDT per knot**, where each cell carries `(sum, count, version_vector)`. Merge is component-wise max on version vector and weighted-sum on `(sum, count)`. Commutative ✓, associative ✓, idempotent ✓ (re-receiving the same `(sum, count, vv)` is a no-op once vv is deduped).
- **Layer B coefficient vector → LWW-Register keyed by `(channel, vv)`**, where the "register" payload is the entire `(θ, κ, P_diag)` triple, and the "winner" by version vector is then κ-weighted-averaged with the local value before write. This is technically a custom register, not a vanilla CRDT, but it preserves SEC because the merge function is deterministic given the same inputs in any order.
- **Peer table → OR-Set CRDT** (observed-remove set, also from Shapiro 2011) for fleet membership, with per-element unique tags so concurrent add/remove resolves correctly.

The recommendation is **hybrid: confidence-weighted blend for layer values, version-vector deduplication for transport-level idempotency, LWW only as a degenerate fallback when both confidences are zero (which should never happen in practice).**

---

## 6. Opt-In UX

ventd's vision is "zero-terminal" — most users should never type a CLI command. Fleet enrollment is intrinsically multi-host, so it cannot be fully zero-terminal, but it can be one short command per host plus a single shared code.

The recommended flow, modelled on the magic-wormhole UX (one-time human-pronounceable code; `wormhole send` / `wormhole receive`, github.com/magic-wormhole/magic-wormhole) and on `tailscale up` (single command joins the mesh, tailscale.com/docs/concepts/what-is-tailscale):

**Host A (initiator):**
```
$ sudo ventd fleet init
fleet 'home' created.
pairing code: 7-cherry-vortex-mango  (valid 5 min)
```

**Host B (joiner):**
```
$ sudo ventd fleet join 7-cherry-vortex-mango
discovering ventd peers via mDNS...
✓ found host 'proxmox-01' on 192.168.1.10
✓ PAKE handshake complete
✓ paired. fleet 'home' now has 2 members.
```

Subsequent hosts repeat the `join` step using the same code (the code is consumed once per join, but `fleet init` can be re-run to mint another code without disturbing existing membership — the long-term static keys do not change).

**Compared alternatives:**

- *smallstep CA `step ca init` + `step ca bootstrap --fingerprint <hex>`* (smallstep.com/docs/step-ca/getting-started, smallstep.com/docs/step-cli/reference/ca/bootstrap) — proven mTLS bootstrap pattern, but it requires the user to copy a 64-hex-character SHA-256 fingerprint between hosts, which is a poor zero-terminal experience and tempts copy-paste mistakes. Excellent reference design but heavyweight for a fan controller.
- *Static `peers.yaml`* — supported as a fallback / "I know what I'm doing" path for sysadmins on segmented VLANs.
- *PIN entry on each host* — rejected: SPAKE2 with a short shared code (per the magic-wormhole design) gives stronger guarantees with the same human effort, since each failed handshake is detected and an attacker only gets one online guess per pairing window.

The code has 16+ bits of entropy via the PGP wordlist (per magic-wormhole.readthedocs.io/en/latest/welcome.html), pairing windows are 5 minutes by default, and the rendezvous is over LAN multicast — there is no "mailbox server" because both endpoints are on the same broadcast domain. If they aren't, fall back to `peers.yaml`.

`ventd doctor` (R13's third surface) reports `fleet: paired (members: 3, last_sync: 47s ago, divergence: 0)`.

---

## 7. Wire Protocol and Transport

**Transport candidates:**

- **HTTPS / mTLS over `crypto/tls`.** Pure Go, in stdlib, CGO-free. Well-understood. The hard part is cert provisioning UX (no central CA). One option: each peer is its own self-signed leaf, and the pairing handshake exchanges and pins the SHA-256 of each side's cert (TOFU pinning, like SSH `known_hosts`). This works and stays inside `crypto/tls`.
- **Noise Protocol Framework (noiseprotocol.org/noise.html, Trevor Perrin, 2018, public domain).** Specifically Noise IK or Noise XX over TCP. Noise IK is what WireGuard uses (`Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`, per the WireGuard whitepaper at wireguard.com/papers/wireguard.pdf, Donenfeld 2017 NDSS) and is well-suited to a setting where both peers know each other's static public keys ahead of time — which is exactly post-pairing ventd. Pure-Go implementation: `github.com/flynn/noise` (BSD-3, GPL-3.0 compatible).
- **WireGuard tunnel.** Rejected as a *requirement*: it forces a kernel module or `wireguard-go` userland, which is deployment friction for a fan controller. Users *may* run ventd over an existing WireGuard or Tailscale interface, but ventd should not require it.
- **SSH-tunneled.** Rejected: requires sshd configuration, key distribution, and a privileged port story; doesn't reduce dependency footprint vs. native Noise.
- **libp2p (`github.com/libp2p/go-libp2p`, MIT, GPL-3.0 compatible per github.com/libp2p/go-libp2p).** Evaluated and rejected as too heavy. libp2p brings a large dependency tree (multiformats, multistream-select, peerstore, AutoNAT, DHT modules) that would balloon the ventd binary well beyond its current single-binary footprint and pull in transitive Go modules with non-trivial CPU/RAM overhead — wrong fit for a Celeron MiniPC.

**Recommendation: Noise IK over TCP for the steady-state transport, falling back to Noise XX during initial pairing (when the peer's static key is unknown).** Pure Go, no CGO, ~5kLOC dependency footprint via `github.com/flynn/noise`. The handshake establishes an AEAD (ChaCha20-Poly1305) channel; thereafter all bundles are framed length-prefixed messages.

**Encoding.** Three options:

- **JSON.** Simple, human-debuggable, stdlib-only. Larger over the wire (~3× CBOR for the same content). Acceptable: a full state bundle is small (kilobytes) and sync is rare (every few minutes).
- **CBOR (RFC 8949).** Compact binary, schema-less, pure-Go libraries available (e.g. `github.com/fxamacker/cbor`, MIT). 2–3× smaller than JSON, modest parsing cost.
- **Protobuf.** Compact, schema'd, but requires `protoc` codegen tooling in the build pipeline — adds friction; the schema-evolution rules are stricter than the additive R20 spec needs.

**Recommendation: JSON for v0.8.0 (simplicity, debuggability, no new dep), with a documented escape hatch to CBOR if bundle sizes become a problem in practice.** State bundles are bounded by the fixed Layer A knot count + Layer B coefficient count per channel (a few hundred floats max); JSON is fine.

---

## 8. Privacy Preservation Within the Fleet

The smart-mode privacy guarantee — "no raw `proc/comm` SHA-256 hashes leave a host without explicit opt-in" — is *strengthened* in R20, not relaxed. Even within a co-administered fleet:

- **Default sharing scope = Layer A + converged Layer B only.** Layer C is opt-in per peer, not per fleet.
- **Per-peer signature-share flags**, persisted as `peer/<id>/share_layer_c=false` by default. A user who wants their NAS and Proxmox host to share workload signatures (because they run similar service stacks) can flip this for that peer pair. Signatures are *never* shared with peers outside the fleet, and never with spec-14 unless individually re-authorised at upload time.
- **Bundle redaction.** Even when Layer C is shared, only `(channel, sig, marginal_benefit, sample_count)` tuples are sent — never `(comm, args, uid, container_id)` or any other raw process attribute. The hash is the same opaque 32-byte value already stored locally per R7.
- **mDNS TXT record minimisation.** The advertised TXT contains only `proto`, `pubkey-fp` (32 hex), `hwfp-prefix` (8 hex), and `state-ver`. No hostname, no fan model, no sensor count — those would leak inventory data to anyone passively listening to multicast on the LAN.
- **AppArmor profile additions.** The R20 patch enumerates: `network inet stream` for the new TCP listener (port-pinned), `network inet dgram` for mDNS (UDP/5353 multicast), and read access to `/var/lib/ventd/fleet/`. No new filesystem write paths beyond the fleet KV namespace.

---

## 9. Relationship to Spec-14 Community Profiles

Spec-14 (curated, internet-scale, opt-in upload, manual review) and R20 (LAN-internal, automatic-on-enrolment, bilateral) share the **hardware fingerprint format** but nothing else. The bridging question is: *can a fleet-merged Layer A be uploaded to spec-14 as "fleet-validated"?*

**Recommendation: No automatic bridge. Fleet-merged state stays local.** Two reasons:

1. **Provenance dilution.** A fleet-merged Layer A is a weighted average across N hosts; spec-14's review process assumes single-host provenance ("this curve worked on this exact box"). Uploading a fleet average would require spec-14 to learn a new provenance type — out of scope for v0.8.0.
2. **Privacy consent transitivity.** A user who opted host A into spec-14 upload may not have opted host B in. Auto-promoting fleet-merged state to spec-14 launders that consent. Cleaner: treat the spec-14 upload as a separate, per-host, manual action that uses *that host's local state* (which may have been influenced by fleet merges, but the host owner explicitly consents to upload).

A future spec amendment (spec-14.1 or new spec-22) could introduce a `fleet-validated` provenance tier with explicit per-host consent, but R20 itself does not reach across.

**Spec-14 must amend** to clarify that uploaded profiles may have been influenced by R20 fleet merges and that this is acceptable as long as upload remains a per-host manual action.

---

## 10. Failure Modes and Recovery

**Partial merge failure (host crashed mid-sync).** The KV+Blob+Log primitives in spec-16 already provide atomic write semantics per key. Merge writes are a single transaction per layer per peer-round; if the host crashes mid-write, the WAL replay on next boot either commits the whole bundle or rejects it (idempotency by `(peer_id, state_ver)` tuple). No partial-state corruption is possible because layer state is only swapped in atomically per channel.

**Stale-state poisoning.** A host with a broken sensor (e.g. tach stuck at 0 RPM at full PWM) will train a degenerate Layer B `θ`. R9's κ identifiability detector should already mark this channel as non-converged, blocking it from being shared (per §4). As a defence-in-depth, R20 adds an **outlier veto**: when receiving Layer B `θ_remote`, compute `||θ_remote − θ_local|| / ||θ_local||`; if the relative norm exceeds a configured threshold (default 3.0) AND `κ_local > κ_remote`, reject the merge and increment `peer/<id>/anomaly_count`. Accumulated anomalies trigger automatic quarantine.

**Rollback.** Spec-16 KV versioning gives us per-key history. R20 stores pre-merge state under `state/<layer>/<channel>@v<state_ver-1>` for one generation back. `ventd fleet rollback --peer <id>` reverts the last merge attributed to that peer. UI surface: `ventd doctor` warns when divergence increases sharply post-merge.

**Per-host quarantine.** `peer/<id>/quarantine=true` blocks all incoming merges from that peer (mDNS announcements still seen, bundles still received but discarded, audit-logged). Set manually by the user, or automatically by the anomaly detector. Quarantine is sticky — surviving daemon restart — until explicitly cleared.

**Network partition.** Eventual consistency (§11) means partition is a non-event: each side's KV is internally consistent; on reunion, gossip resumes and version vectors deduplicate. No split-brain hazard because no layer write *requires* a quorum.

---

## 11. CAP-Theorem Positioning

ventd is a fan controller. Fan-curve drift on the order of seconds to minutes does not endanger hardware (the spec-04 thermal safety floor is enforced *locally* and is never federated — it is per-host hardcoded fallback that runs even if all fleet state is corrupted).

**Recommendation: eventual consistency, AP in CAP terms.** Justification:

- **Availability is critical.** The fan controller must always run; if peer sync is unreachable, the local control loop continues unimpaired.
- **Partition tolerance is forced.** LAN partitions happen routinely (Wi-Fi reboots, switch reboots, host suspends). The system must keep working.
- **Strong consistency is unnecessary.** There is no transactional invariant across hosts. "Host A's Layer A and Host B's Layer A should be exactly equal" is a *nice-to-have*, not a *must-have*. They will converge to the same fixed point as long as both are sampling similar workloads.
- **CRDT framing makes this rigorous.** Per Shapiro et al. (inria.hal.science/inria-00609399), a system whose merges are commutative-associative-idempotent achieves Strong Eventual Consistency without coordination — exactly what we want.

The local thermal-safety controller (spec-04) is a separate concern: it is a strongly-consistent local invariant ("temp > T_critical → PWM = 100%") that requires no network at all. R20 cannot weaken it.

---

## 12. Out of Scope for R20

Explicitly out of scope, to be re-litigated in later R-rounds or rejected:

- **Internet-scale federation.** Spec-14 territory. R20 is link-local only.
- **Cross-user fleets.** Sharing Layer A between two different humans' homelabs. Requires a different trust model and is not requested.
- **Untrusted peers.** R20 assumes co-administration. Defending against actively malicious peers inside the fleet is not a goal; defence is limited to "broken sensor" / "stuck tach" / "rooted host that started lying" classes, mitigated by the per-layer outlier vetoes.
- **ML-style gradient federation.** No SGD, no per-round gradient transport, no FedAvg coordinator. Just structured state sync.
- **Tailscale/WireGuard mesh dependence.** Optional integration, not a requirement.
- **Automatic upload of fleet-merged state to spec-14.** Manual, per-host, separate consent action.
- **Multi-fleet membership.** A host belongs to exactly one fleet at a time. (Future R-round may relax this.)
- **Live coordinated experiments** (e.g. "all three hosts ramp PWM together to characterise an HVAC effect"). Synchronous distributed actions = orchestrator pattern = explicitly forbidden.

---

# Spec-Ready Findings Appendix

## Algorithm choice + rationale

- **Discovery:** mDNS / DNS-SD per RFC 6762 + RFC 6763 on `_ventd-fleet._tcp.local`, TXT records minimised (proto, pubkey-fp, hwfp-prefix, state-ver). Pure-Go zeroconf library, BSD-3 (GPL-3.0 compatible).
- **Bootstrap pairing:** SPAKE2-style short pronounceable code (magic-wormhole-inspired), 16-bit minimum entropy, 5-minute window, single-use, exchanged out-of-band by the human.
- **Long-term auth:** Static Curve25519 keypair per host, SHA-256-fingerprint pinned into the per-peer KV namespace at first pairing (TOFU after PAKE).
- **Transport:** Noise IK (steady state) / Noise XX (initial pairing) over TCP, AEAD = ChaCha20-Poly1305, hash = BLAKE2s. Library: `github.com/flynn/noise` (BSD-3). Pattern selection mirrors WireGuard (wireguard.com/papers/wireguard.pdf) since both peers know each other's static key post-pairing.
- **Encoding:** JSON for v0.8.0 (simplicity, debuggability, stdlib-only); CBOR escape hatch documented but deferred.
- **Merge — Layer A:** confidence-weighted average per knot, version-vector deduplicated, family-tier eligible. CvRDT-shaped.
- **Merge — Layer B:** κ-gated weighted average of `θ` only when both peers converged; warm-start adoption when one peer is unconverged; diagonal-only `P` priors to preserve PSD; exact-tier eligible only.
- **Merge — Layer C:** opt-in per peer, default off; exact-tier only; only opaque hashes leave the host.
- **Conflict resolution:** confidence-weighted blend with version-vector dedup; LWW only as degenerate fallback.
- **Consistency:** eventual (AP), Strong Eventual Consistency in the CRDT sense; local spec-04 thermal safety remains strongly consistent and outside the federation envelope.

## State shape / RAM impact

Additive only on top of the locked R1–R19 shape. New KV namespace `fleet/`:

- `fleet/identity` — local static pubkey (32 B) + privkey-encrypted (96 B sealed) + fleet name (≤32 B). One-time write.
- `fleet/peers/<peer_id>/` — per peer:
  - `pubkey` (32 B), `hwfp` (32 B), `tier` (1 B enum), `last_seen_unix` (8 B), `state_ver` (8 B), `anomaly_count` (4 B), `quarantine` (1 B), `share_layer_c` (1 B), `address_cache` (≤64 B). **Total ≈ 200 B per peer.**
- `fleet/wal/` — append-only Log of merge events (peer_id, layer, state_ver, accepted/rejected, reason). Bounded ring buffer, 4 KiB default.
- Working set in RAM:
  - Peer table at ≈200 B × N peers; for N=10, 2 KiB.
  - One outstanding state bundle per peer during sync, ≤ 8 KiB; serialised, freed on commit.
  - mDNS responder cache, ≤ 4 KiB.
  - Noise session state, ≈ 1 KiB per active session.
- **Total worst-case incremental RAM: ≤ 32 KiB for a 10-peer fleet.** Negligible on Celeron.

CPU: mDNS responder is event-driven (idle cost near zero). Sync cycle every 60 s default; one Noise handshake (~200 µs on Celeron) + one JSON encode/decode (~50 µs for ≤8 KiB) + one KV write transaction (~1 ms). Per-minute CPU budget for fleet code on Celeron MiniPC: well under 0.1%.

## RULE-FED-* invariant bindings (1:1 with subtests)

- **RULE-FED-001 — DiscoveryLinkLocalOnly.** mDNS responder MUST bind only to link-local interfaces; MUST set IP TTL/hop-limit = 255 per RFC 6762 §11.
- **RULE-FED-002 — TXTRecordMinimal.** Advertised TXT MUST contain only the four fields {proto, pubkey-fp, hwfp-prefix, state-ver}; any additional field is a test failure.
- **RULE-FED-003 — PairingCodeSingleUse.** A pairing code MUST be invalidated after one successful join OR after the 5-minute TTL, whichever comes first.
- **RULE-FED-004 — StaticKeyPinned.** Post-pairing, MUST refuse any session whose static pubkey does not match the pinned `peer/<id>/pubkey`.
- **RULE-FED-005 — HwfpTierGate.** Layer B/C merges MUST be rejected when `tier ≠ EXACT`. Layer A merges MUST be rejected when `tier == REJECT`.
- **RULE-FED-006 — KappaGateLayerB.** A Layer B merge from a peer MUST NOT contribute to the local `θ` blend if the *peer's* reported κ for that channel is below the converged threshold; warm-start adoption only.
- **RULE-FED-007 — LayerCDefaultOff.** `peer/<id>/share_layer_c` MUST default to `false`; setting to `true` requires an explicit user action (CLI flag or config); never inferred.
- **RULE-FED-008 — RawSignatureNeverLeaves.** Bundle serialiser MUST refuse to emit any field other than the opaque 32-byte sig hash, sample count, and marginal benefit per Layer C entry. Source process names MUST NOT appear in any wire bundle.
- **RULE-FED-009 — VersionVectorIdempotent.** Re-receiving a bundle with `(peer_id, state_ver)` already in the WAL MUST be a no-op.
- **RULE-FED-010 — OutlierVetoLayerB.** A Layer B merge proposal where `||θ_remote − θ_local|| / ||θ_local|| > 3.0` AND `κ_local > κ_remote` MUST be rejected and `anomaly_count` incremented.
- **RULE-FED-011 — QuarantineSticky.** A peer with `quarantine=true` MUST have all incoming bundles dropped (audit-logged), surviving daemon restart until explicitly cleared.
- **RULE-FED-012 — RollbackOneGeneration.** `ventd fleet rollback --peer <id>` MUST restore each affected layer to its `@v<state_ver-1>` snapshot atomically.
- **RULE-FED-013 — LocalSafetyImmune.** Spec-04 thermal safety floor MUST be enforced from per-host hardcoded values; no fleet code path may modify it.
- **RULE-FED-014 — AppArmorEnumerated.** The AppArmor profile MUST list exactly the new capabilities required (TCP listener on port `P`, UDP/5353 multicast, `/var/lib/ventd/fleet/` rw); any other network or fs access from fleet code paths MUST be denied by profile and tested.
- **RULE-FED-015 — Spec14NoAutoBridge.** Fleet-merged state MUST NOT be automatically uploaded to spec-14 endpoints; upload MUST require a separate per-host manual action.

## Doctor surface contract

`ventd doctor` (per R13's three-surface design: Internals + RECOVER + live-metrics) gains a fourth-quadrant **Fleet** section, only printed when `fleet/identity` exists:

```
FLEET
  identity:    home (this host: laptop-thinkpad / fp:a3c4..)
  members:     3 / 3 reachable
    proxmox-01  192.168.1.10  tier=EXACT  last_sync=47s  div=0.02
    nas-truenas 192.168.1.20  tier=FAMILY last_sync=2m   div=0.04
    minipc-cel  192.168.1.30  tier=EXACT  last_sync=12s  div=0.01  [share_layer_c=on]
  enrollment:  paired (2025-09-14)
  warnings:    none
  actions:     ventd fleet pair | ventd fleet rollback | ventd fleet quarantine <id>
```

When degraded:

```
  warnings:
    - peer 'nas-truenas' anomaly_count=2 (last: layer-B outlier veto, channel pwm2)
    - peer 'minipc-cel' offline 14m (last sync stale)
```

The Internals surface gains a `fleet/` subtree dump (peer table contents, WAL tail). The RECOVER surface gains `--reset-fleet` (clears `fleet/` namespace, returns host to standalone). The live-metrics surface streams `fleet.sync_count`, `fleet.merge_accepted`, `fleet.merge_rejected{reason=...}`, `fleet.divergence{layer,channel}`.

## HIL validation matrix (Phoenix's testing fleet)

| # | Test | Hardware | Status |
|---|------|----------|--------|
| 1 | mDNS announce/respond on isolated `/24` | Proxmox host + 1 VM | Runnable now |
| 2 | Pairing code single-use enforcement | 2 Proxmox VMs | Runnable now |
| 3 | TOFU rejection of mismatched static key | 2 Proxmox VMs (recreate one) | Runnable now |
| 4 | Layer A confidence-weighted merge, identical hwfp | 2 Proxmox VMs with same DMI passthrough | Runnable now |
| 5 | Layer B κ-gated warm-start adoption | 2 Proxmox VMs, force κ=0 on one | Runnable now |
| 6 | Hwfp tier=REJECT blocks all merges | Proxmox VM ↔ Steam Deck | Runnable now (Steam Deck has different hwmon set) |
| 7 | Hwfp tier=FAMILY allows Layer A only | Two laptops same model, different BIOS minor | **Blocked** — Phoenix has 3 laptops but not two of the same model |
| 8 | Outlier veto on broken-tach scenario | Proxmox VM + simulated stuck tach | Runnable now (use sysfs hwmon stub) |
| 9 | Quarantine sticky across daemon restart | 2 Proxmox VMs | Runnable now |
| 10 | Rollback restores prior generation | 2 Proxmox VMs | Runnable now |
| 11 | 5-peer concurrent gossip convergence | 5 Proxmox VMs cloned from same template | Runnable now (Phoenix's lab is ideal) |
| 12 | Network-partition heal (eventual consistency) | 2 Proxmox VMs + `iptables` partition | Runnable now |
| 13 | AppArmor profile denies non-enumerated network | MiniPC Celeron | Runnable now |
| 14 | Layer C default-off, opt-in flag honoured | 2 Proxmox VMs | Runnable now |
| 15 | TXT record minimisation (passive sniff) | Any LAN host + tcpdump | Runnable now |
| 16 | Bundle serialiser refuses raw `comm` strings | Unit test, not HIL | Runnable now |
| 17 | Cross-architecture peer (x86 ↔ Steam Deck) Layer A only | Proxmox + Steam Deck | Runnable now (both Linux, different hw) |
| 18 | Celeron CPU budget under 1% with 5 peers | MiniPC Celeron + 4 Proxmox VMs as peers | Runnable now |

**Summary: 17 of 18 cases runnable on Phoenix's existing fleet immediately. The Proxmox VM cloning capability is genuinely ideal for this — 5 peers from one template image is one `qm clone` loop.** Only the same-model-different-BIOS case (#7) is hardware-blocked and is a low-priority test (Tier-2 family-match path).

## Estimated CC cost (Sonnet, single-PR target $10–30)

Recommended split: **two PRs, sequenced.** A single PR is too large to keep under $30 with the AppArmor + new network code + new test suite.

**PR 1 — "fleet/transport+discovery"** (target ~$18)
- ~1200 LOC new Go: `internal/fleet/discovery` (mDNS responder + browser, ~400 LOC), `internal/fleet/transport` (Noise IK/XX wrapper around `flynn/noise`, ~350 LOC), `internal/fleet/pair` (PAKE-style code generation + verifier, ~300 LOC), wiring + lifecycle (~150 LOC).
- 1 new dep: `github.com/flynn/noise` (BSD-3). 1 new dep: `github.com/grandcat/zeroconf` (BSD-3) OR a hand-rolled minimal mDNS responder if footprint matters more.
- ~600 LOC tests covering RULE-FED-001 through 004, 014, 015.
- AppArmor profile diff: +6 lines (UDP 5353, TCP listener, `fleet/` r/w).
- Doctor surface: stub only, prints "fleet: not enrolled" until PR 2.

**PR 2 — "fleet/merge+state"** (target ~$22)
- ~1500 LOC new Go: `internal/fleet/merge` (Layer A/B/C merge functions, ~500 LOC), `internal/fleet/peerstate` (KV namespace wrapper, ~300 LOC), `internal/fleet/anomaly` (outlier veto + quarantine state machine, ~250 LOC), `internal/fleet/wal` (merge log, ~150 LOC), CLI commands `fleet init/join/rollback/quarantine/status` (~300 LOC).
- 0 new deps.
- ~900 LOC tests covering RULE-FED-005 through 013.
- Doctor surface: full implementation per contract above.

Total estimate: **~$40 across two PRs**, both under the $30/PR cap, deliverable independently (PR 1 lands functioning enrollment+transport with no merge; PR 2 lights up the merge logic).

## Spec target version

**v0.8.0** at the earliest. Strict prerequisites:

- v0.6.0 smart-mode bootstrap shipped (R17 RLS + R9 κ identifiability are real, not stubbed).
- v0.7.x stabilises spec-16 KV/Blob/Log primitives and the R13 doctor three-surface design.
- v0.8.0 then introduces R20 fleet federation as additive on top, with no R1–R19 state-shape changes.

If smart-mode v0.6.0 slips, R20 slips with it; there is no path to ship R20 before Layer B is real, because κ-gated merge has no meaning without κ.

## Actionable conclusions — decisions Phoenix must make

1. **Confirm Noise IK over `flynn/noise` vs. mTLS over stdlib `crypto/tls`.** Noise is recommended above; the alternative is purely stdlib but worse UX for cert pinning. Decide before PR 1 begins.
2. **Confirm mDNS library: `grandcat/zeroconf` (~1500 LOC dep) vs. hand-rolled minimal responder (~400 LOC, no dep).** Hand-rolled keeps the binary minimal and aligns with single-binary discipline; `grandcat/zeroconf` is faster to land. Phoenix's call.
3. **Confirm pairing-code wordlist source.** PGP wordlist (used by magic-wormhole) is in the public domain and ~500 lines of static data. EFF wordlist is an alternative. Decide.
4. **Add to `spec-smart-mode.md`:** explicit subsection "Privacy boundary across LAN federation," restating that Layer C signature sharing remains opt-in even within fleets, and that `proc/comm` raw strings *never* serialise into a wire bundle. Cross-link to spec-R20.
5. **Amend `spec-14.md`** with a single paragraph: uploaded profiles MAY have been influenced by R20 fleet merges; upload remains per-host manual; no automatic bridge from R20 to spec-14 is provided in v0.8.0.
6. **Amend `spec-16.md`** with the new `fleet/` KV namespace declaration, including the per-peer schema and the 4 KiB WAL ring-buffer specification.
7. **Draft new spec `spec-R20-fleet.md`** as the canonical reference, using this artifact as the input. Should include the full RULE-FED-* table, the wire format (JSON schema for the bundle), and the AppArmor profile diff.
8. **Decide on Layer C default policy:** the recommendation is "opt-in per peer," not "opt-in per fleet." Confirm.
9. **Decide on multi-fleet membership policy:** recommendation is one-fleet-per-host for v0.8.0; explicitly reject in spec.
10. **Decide whether `ventd fleet leave` is reversible** (i.e., does it tombstone the static keypair or just unpair? Recommendation: keypair persists; only the peer table is wiped, so re-pairing the same hosts is one-shot.)
11. **Schedule the Proxmox 5-VM cloned-template lab build** for HIL test #11 — this is the test that will catch most multi-peer convergence bugs and Phoenix's lab is uniquely positioned for it.

---

# Consolidation Note: Mapping R16–R20 onto v0.7.x → v1.0

R16–R20 are the post-bootstrap R-rounds. They cannot land until smart-mode v0.6.0 ships, because each presupposes a real Layer B (R17 RLS) with real κ identifiability (R9). Tentative sequencing:

| Round | Topic (one-liner) | Target version | Depends on |
|---|---|---|---|
| R16 | Smart-mode telemetry hooks for spec-13 metrics export | v0.6.1 | v0.6.0 ships |
| R17 | RLS-based Layer B coupling coefficients | v0.6.0 (already in v0.5.x→v0.6.0 sequence) | spec-smart-mode core |
| R18 | Layer C signature overlay marginal-benefit accounting | v0.7.0 | R17 |
| R19 | Doctor surface refinements + recovery flows for smart-mode | v0.7.x | R13 + R17 |
| R20 | LAN-internal fleet federation (this artifact) | v0.8.0 | R17 + R19 + spec-16 stable |

**Conflicts with the existing 11-patch v0.5.x → v0.6.0 sequence (smart-mode bootstrap):**

- **No direct conflict.** R20 is additive and purely post-v0.6.0. None of the 11 bootstrap patches touch network code or peer state, so there is no merge contention.
- **Soft conflict #1: state-shape lock.** Patches 7–9 of the v0.5.x sequence finalise the Layer B `(θ, P, κ)` tuple shape and the Layer C `(channel, sig)` overlay shape. R20 takes a hard dependency on these being stable. If those patches slip or reshape, R20's merge functions reshape with them. Mitigation: do not start PR 1 of R20 until the state-shape lock lands and is tagged in v0.6.0-rc.
- **Soft conflict #2: doctor surfaces.** R19 finalises the R13 three-surface model (Internals + RECOVER + live-metrics). R20 adds a fourth section (Fleet). If R19's surface registry is not pluggable, R20 will need to amend it. Mitigation: ensure R19 introduces a `doctor.RegisterSurface(name, fn)` hook so R20 can attach without modifying R19's core code.
- **Soft conflict #3: AppArmor profile.** The bootstrap sequence freezes the AppArmor profile around patch 10. R20 needs amendments (UDP/5353, TCP listener, new fs paths). Mitigation: structure the AppArmor profile as a base + optional fragments file pattern in v0.6.0 so R20 ships a fragment, not a full-profile diff.
- **No conflict with spec-14.** Spec-14 is independent; the R20 amendment to spec-14 is a one-paragraph clarification about provenance, not a behavioural change.

**Release-train recommendation:**

- **v0.6.0** ships smart-mode (R17 baseline + R9 κ + locked state shape).
- **v0.6.1** ships R16 telemetry hooks; small patch.
- **v0.7.0** ships R18 Layer C marginal-benefit + R19 doctor refinement. Both are local-only; no new network surface.
- **v0.7.x** stabilises spec-16 KV/Blob/Log; introduces the surface-registry hook for the doctor.
- **v0.8.0** ships R20 fleet federation as two PRs (transport+discovery, then merge+state) on top of a fully stable v0.7.x base.
- **v1.0** is then a stabilisation release: spec-14 amendment, spec-R20 promoted to mainline, no new R-round work; all RULE-FED-* tests passing on Phoenix's lab; HIL matrix runs green on Proxmox + MiniPC + Steam Deck.

The critical sequencing rule: **R20 cannot ship before R19, and R19 cannot ship before R17.** Any attempt to parallelise R20 with the v0.5.x → v0.6.0 bootstrap will hit the state-shape-lock dependency and waste effort. The 11-patch bootstrap sequence proceeds first; R20 is a v0.8.0 horizon item.