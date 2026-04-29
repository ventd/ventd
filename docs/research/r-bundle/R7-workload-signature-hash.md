# R7 — Workload Signature Hash for Layer C of Smart-Mode

## Executive Summary

Layer C of ventd's smart-mode learns a per-workload marginal-benefit function for each (channel, workload-signature) pair, where the signature is derived from the names of the processes currently driving load on the system. R7 specifies how that signature is computed, stored, evicted, and protected. The recommended design is:

1. **Hash function:** keyed **SipHash-2-4** (`github.com/dchest/siphash`, public-domain, pure-Go, CGO-free) with the per-install salt as the 16-byte key. The output is truncated to 64 bits and rendered as a hex token. Rationale: SipHash-2-4 is the canonical short-input keyed PRF; it is the same primitive Go's runtime uses internally in `hash/maphash`; it is faster than HMAC-SHA-256 on 16-byte inputs by an order of magnitude; it is purpose-built to resist key-recovery and pre-image-by-collision attacks against attacker-controlled short inputs (which is exactly the threat model of a leaked diag bundle); and it is a single, audited 200-line file with no SIMD or assembly path that could break under `CGO_ENABLED=0`.
2. **Top-N criterion:** **Hybrid CPU-OR-RSS gating with EWMA-weighted hash multiset, NOT a per-tick top-N snapshot.** A process contributes to the signature if its EWMA CPU-share over the last ~10 s exceeds 5 % of one core *or* its RSS exceeds 256 MiB, *and* it is not on the maintenance blocklist (shared with R5). The signature itself is the **top-K=4 hashes by EWMA weight**, with weight = Σ (CPU-share × decay^age). The signature only "promotes" to the active label after K is stable for ≥3 consecutive 2 s ticks (matched to R11's fast-loop window).
3. **Hash inputs:** **comm only** (16-byte kernel-truncated name). Adding `exe` symlink, `cmdline`, or `parent-comm` either expands the privacy surface (cmdline leaks secrets and home paths; exe leaks `/home/$USER/...`), produces unstable output (parent-comm differs between desktop launcher, terminal, systemd-run, and Steam), or duplicates information already captured by comm. The 16-byte cap is explicitly designed by the kernel as a bounded, stable identifier and is exactly the input shape SipHash is optimised for.
4. **Stability:** the four named flap scenarios (Steam launch, gcc/cc1/ld churn, Chrome Site-Isolation tab churn, systemd-resolved cycling — and we should note that systemd-resolved does *not* in fact cycle in normal operation, see Q4) are quantified below; the EWMA-multiset + K-stable-promotion design absorbs the dominant flap modes while remaining responsive to genuine workload transitions on the order of 6–10 s.
5. **Capacity:** **128 buckets**, weighted-LRU eviction (score = `hit_count × exp(-age/τ)`, τ = 14 days), persisted via spec-16 KV shape under `workload-sig/<hex>`. Library is reload-on-start; EWMA weights resume from saved state.
6. **Privacy:** 32-byte per-install salt at `/var/lib/ventd/.signature_salt` (mode 0600, ventd:ventd, never shipped in diag bundles, never logged), used as the SipHash key. No network egress. Operator escape hatch is `ventd doctor --reveal-signatures`, which dumps the live comm→hash table to stdout, requires `ventd ctl pause` to have been issued first, and never persists the mapping.

This document is structured as the long-form research write-up (Part 1), the spec-ingestion appendix block (Part 2), and the implementation file targets (Part 3), mirroring the R1–R11 methodology already established in `ventd-smart-mode-research-bundle.md`.

---

## Methodology

The research mandate was to weigh four hash candidates against ventd's specific operating envelope (Linux-only, `CGO_ENABLED=0`, 1 Hz sampling, ~hundreds of processes, leaked-diag-bundle threat model), to ground the top-N design against published process-model documentation for the four workloads named in the brief, and to align persistence and privacy decisions with the already-locked spec.

Primary sources consulted:

- The Linux man-pages project's `proc_pid_comm(5)` page and the kernel's `Documentation/filesystems/proc.txt` (kernel.org) for the `TASK_COMM_LEN = 16` invariant.
- The Go standard library's `hash/maphash` source on `go.dev` for confirmation that the runtime's "fast" hash is process-local, non-portable, and explicitly *not* cryptographically seeded across process boundaries.
- The Chromium project's *Process Model and Site Isolation* design document and its *Site Isolation Design Document* on `chromium.googlesource.com` and `chromium.org` for the renderer-process model.
- The Valve `ValveSoftware/Proton` repository and `steam-for-linux` issue tracker for empirical Steam/Proton process spawn behaviour ("Adding process N for gameID …" / "Removing process N…" telemetry lines).
- The Linux Kernel Organization's `Documentation/kbuild/llvm.html` and the GNU/LLVM toolchain manuals to characterise the cc1/as/ld call chain for the gcc/cc1/ld false-flag scenario.
- The systemd-resolved `service(8)` Debian and Ubuntu manpages for the daemon's lifetime model (it is a long-lived stub, not a respawned worker).
- Verma et al., *Large-scale cluster management at Google with Borg* (EuroSys 2015) and the Google cluster trace dataset description for the academic baseline on workload classification.
- The xxHash project (`xxhash.com`, `Cyan4973/xxHash`) and Go ports `cespare/xxhash`, `zeebo/xxh3`.
- The BLAKE3 reference and Go ports `zeebo/blake3` (CC0) and `lukechampine/blake3`.
- The SipHash specification (Aumasson & Bernstein, 2012) and Go ports `dchest/siphash` (public domain) and `aead/siphash`.
- RFC 2104 (HMAC) and the Wikipedia article on SipHash for the keyed-vs-HMAC construction comparison.
- USPTO Patent 7,493,419 ("Input/output workload fingerprinting for input/output schedulers") for prior art on classifying-by-fingerprint.

Where source-snippet quality was thin (notably for systemd-resolved cycling, where the only "evidence" online is bug reports of the daemon failing-to-resolve, *not* of the daemon being re-spawned), the report flags the gap rather than treating speculation as fact.

---

## Q1. Hash Function Selection

### Threat model recap

The signature hash sees a small, low-entropy input space — the universe of all `/proc/PID/comm` values that have ever existed on Linux. The kernel enforces `TASK_COMM_LEN = 16` (15 printable bytes plus terminator), and the realistic working set across all distributions is on the order of low thousands of distinct names (see the man-page reference: *"Strings longer than `TASK_COMM_LEN` (16) characters (including the terminating null byte) are silently truncated"*). That is small enough that a *plain* unkeyed hash of any algorithm — SHA-256 included — admits trivial rainbow-table reversal: an attacker who exfiltrates a diag bundle, learns the hash function, and pre-computes `H("chrome")`, `H("cc1")`, `H("ffmpeg")`, … recovers comm names instantly. The privacy property therefore comes *entirely* from a per-install secret, not from the choice of algorithm.

That observation flips the candidate ranking. We are not choosing between "secure" and "insecure" hashes; every candidate is equally insecure unkeyed, and equally rainbow-resistant when keyed. The real axes are:

| Axis | What it favours |
|---|---|
| Pure-Go portability under `CGO_ENABLED=0` | All four. None requires CGO. |
| Output stability across kernel/Go versions | All four. Every candidate is byte-deterministic given input + key. |
| Native support for keyed mode | SipHash (built for it), HMAC-SHA-256 (standard construction), BLAKE3 (built-in keyed mode), xxh3 (seeded but not a MAC). |
| Speed on 16-byte inputs | xxh3 ≈ SipHash ≈ stdlib `hash/maphash` ≫ BLAKE3 ≫ SHA-256. |
| Provable indistinguishability from PRF on short inputs | SipHash (its design objective), HMAC-SHA-256 (proven). xxh3 makes no claim. |
| Code surface / audit cost | SipHash (~200 LOC). xxh3 (much larger with assembly). BLAKE3 (large). SHA-256 (stdlib). |
| Resistance to length-extension if an attacker controls salt placement | SipHash (none — it's a PRF, not Merkle–Damgård), BLAKE3 (none), HMAC-SHA-256 (none, by construction). Naive `SHA256(salt‖comm)` is *vulnerable* to length-extension but practically not exploitable here because the input is fixed-length. |

### Detailed candidate analysis

**SHA-256 (`crypto/sha256`).** The default safe answer for cryptographic hashing in Go. It is hardware-accelerated on amd64 (`SHA-NI`) when the CPU supports it and on ARM64 (`SHA2`); Go has shipped this acceleration since 1.6 (amd64) and 1.16 (arm64). However, it is *not* a keyed primitive — to defeat rainbow-table reversal of leaked diag bundles, R7 would have to wrap it in HMAC (RFC 2104). HMAC-SHA-256 over a 16-byte input requires two compression-function calls plus block padding, putting throughput in the ~150–300 ns range per hash. That is fine for 1 Hz × 200 processes (200 × 200 ns = 40 µs per tick), but it is roughly 10× slower than SipHash for no privacy benefit on this workload. The construction also produces a 256-bit digest that is wasteful — we will truncate it to 64 bits anyway. Recommend: not the primary choice, but acceptable as a fallback if a future audit rejects SipHash for any reason.

**BLAKE3.** Two pure-Go ports exist. `github.com/zeebo/blake3` (CC0-1.0) and `lukechampine.com/blake3` both ship Plan9-syntax assembly for AVX2/SSE4.1/AVX-512 — crucially, this is *Go's own assembler*, not C compiled via cgo, so both ports build cleanly under `CGO_ENABLED=0`. Per the zeebo/blake3 README, hashing rate caps at ~1 KiB inputs and the hasher state initialisation costs ~100 ns in absolute terms; for 16-byte inputs BLAKE3 is in fact *slower* than SipHash because the state setup dominates. BLAKE3 has a native keyed mode (`blake3.NewKeyed(key[32]byte)`) and a derive-key mode that would be elegant for the salt-as-key construction. The downsides are: (a) it is a much larger code surface than SipHash and (b) its speed advantage materialises only at multi-KiB input sizes that ventd will never see. The BLAKE3 author has open-issued an x/crypto adoption proposal (`golang/go#36632`); it has not landed as of Go 1.25. Recommend: not the primary choice; the only argument for it is "newest cryptographic hash" and that is not a value-driver here.

**xxHash / xxh3 (`github.com/cespare/xxhash/v2`, `github.com/zeebo/xxh3`).** xxh3 is the fastest 64-bit hash on small inputs in any language, in the ~1–5 ns range per 16-byte hash on modern x86. Both ports build under `CGO_ENABLED=0` (`cespare/xxhash` ships Plan9 amd64/arm64 assembly with a `purego` build tag for fallback; `zeebo/xxh3` similarly). xxh3 has a 64-bit secret-seed mode and a 192-byte custom-secret mode that *can* be used as a keyed PRF, but the xxHash author does not claim cryptographic indistinguishability for it, and the construction has not seen formal MAC-style analysis. For a workload where collision tolerance is "Layer C self-corrects on misclustering" but rainbow-table reversal of leaked hashes *is* a stated threat, xxh3 is fast enough but is the wrong instrument: it solves the speed problem we don't have at the cost of the proof we do want. Recommend: not the primary choice.

**SipHash-2-4.** The exact tool the brief is asking for. It was published in 2012 by Aumasson and Bernstein specifically to defend hash tables against adversarial short-input flooding (HashDoS) — i.e., its threat model is "an attacker controls short inputs and learns hash outputs; can they recover the key?" That is *literally* the leaked-diag-bundle threat model, with "key" replaced by "salt". It is keyed by construction (16-byte key), produces a 64-bit output that suits ventd's needs natively (no truncation theatre), runs in ~10–40 ns per 16-byte input in pure Go, and is the same primitive Go's own `runtime.maphash` uses on platforms without aeshash. The reference Go implementation `github.com/dchest/siphash` is ~200 LOC of pure Go, public-domain (CC0), and has been stable since 2013. The `aead/siphash` port adds a `hash.Hash` interface wrapper if needed.

The Wikipedia entry on SipHash summarises the security claim concisely: *"SipHash, however, is not a general purpose key-less hash function … and therefore must always be used with a secret key in order to be secure. … SipHash instead guarantees that, having seen X<sub>i</sub> and SipHash(X<sub>i</sub>, k), an attacker who does not know the key k cannot find (any information about) k or SipHash(Y, k) for any message Y not in {X<sub>i</sub>} which they have not seen before."* That is exactly the property R7 needs: an attacker who exfiltrates a leaked diag bundle (which contains many `(comm, hash)` pairs of the *attacker's known* comm names) cannot extend the table to comms they did not already see — including comms unique to the user's machine such as custom binary names or container-internal process names.

The relationship to `hash/maphash` deserves a note: the Go stdlib `hash/maphash` package is **not** suitable for ventd's use case despite using SipHash-family primitives internally, because its `Seed` value is documented as *"local to a single process and cannot be serialized or otherwise recreated in a different process"* (`go.dev/src/hash/maphash/maphash.go`, lines 29–30). On daemon restart, ventd needs the hashes saved in `/var/lib/ventd/state.kv` to remain meaningful; only an explicit user-supplied key satisfies that — which is exactly what `dchest/siphash`'s `Hash(k0, k1, b)` API exposes.

### Recommendation

Use **SipHash-2-4 keyed with the per-install salt**, via `github.com/dchest/siphash`'s `Hash(k0, k1, []byte(comm)) uint64` fast-path. Render the 64-bit output as 16 lowercase hex characters; use this as the canonical `<hex>` token in storage keys, log lines, and diag bundle entries. Do *not* truncate further; 64 bits provides a birthday-bound of ~4 billion before a 50 % collision probability, which is enormously above the realistic working set of distinct comm values.

```go
// internal/signature/hash.go
package signature

import (
    "encoding/binary"
    "github.com/dchest/siphash"
)

type Hasher struct {
    k0, k1 uint64 // derived from the 32-byte salt by splitting
}

func NewHasher(salt []byte) *Hasher {
    if len(salt) < 16 {
        panic("signature: salt must be at least 16 bytes")
    }
    return &Hasher{
        k0: binary.LittleEndian.Uint64(salt[0:8]),
        k1: binary.LittleEndian.Uint64(salt[8:16]),
    }
}

// HashComm returns the canonical 64-bit SipHash-2-4 of the kernel comm name
// under the install salt. Output is rendered as lowercase hex by the caller.
func (h *Hasher) HashComm(comm string) uint64 {
    return siphash.Hash(h.k0, h.k1, []byte(comm))
}
```

Note: only the first 16 bytes of the 32-byte salt are used as the SipHash key (SipHash takes a 128-bit key). The remaining 16 bytes are reserved for forward compatibility — for example, derivation of a separate per-channel sub-key under HKDF if a future spec revision wants per-channel hash domain separation. Do not delete the unused bytes from the salt file.

### Pure-Go status of all candidates (CGO matrix)

| Library | Module path | License | CGO required? | SIMD path | Pure-Go fallback |
|---|---|---|---|---|---|
| stdlib SHA-256 | `crypto/sha256` | BSD-3 (Go) | No | Plan9 asm (SHA-NI) | yes |
| zeebo/blake3 | `github.com/zeebo/blake3` | CC0-1.0 | No | Plan9 asm AVX2/SSE4.1 | yes |
| lukechampine/blake3 | `lukechampine.com/blake3` | MIT | No | Plan9 asm AVX-512/AVX2 | yes |
| cespare/xxhash | `github.com/cespare/xxhash/v2` | MIT | No | Plan9 asm amd64/arm64 | `purego` build tag |
| zeebo/xxh3 | `github.com/zeebo/xxh3` | BSD-3 | No | Plan9 asm amd64 | yes |
| dchest/siphash | `github.com/dchest/siphash` | CC0-1.0 | No | none (not needed) | n/a |
| aead/siphash | `github.com/aead/siphash` | MIT | No | none | n/a |

All seven ship under `CGO_ENABLED=0`; the brief's concern that "pure-Go SIMD is rare" is technically correct for handwritten C-style intrinsics, but Go's Plan9 assembler is invisible to `CGO_ENABLED` and ships in every one of the libraries above. None of these libraries are at risk of breaking ventd's build matrix.

---

## Q2. Top-N Selection Criteria

### What top/htop/ps actually compute

`top(1)` and `htop(1)` derive their CPU% column from `/proc/[pid]/stat` fields 14 and 15 (utime, stime), divided by the wall-clock interval since the previous sample, divided by `sysconf(_SC_CLK_TCK)` (jiffies/sec, typically 100 or 1000), times 100. The unit is "% of one core" — so a 16-thread workload pegging an 8-core CPU shows up as 1600 %. `ps --sort=-%cpu` differs subtly: it computes lifetime average CPU%, i.e. `(utime + stime) / (now - starttime)` as a percentage. That makes ps a poor choice for "what is the workload *right now*"; it will rank a process that ran briefly at 100 % an hour ago above a process consuming 90 % continuously for the last 30 s, *unless* the older process has been alive long enough that its lifetime average has decayed. ventd should not emulate ps. It should emulate top: instantaneous CPU% over the most recent sampling interval.

### What the academic literature says about defining "the workload"

Verma et al.'s Borg paper (EuroSys 2015, *Large-scale cluster management at Google with Borg*) does not classify workloads by per-process names at all; Borg knows the workload because users *declared* it as part of their job submission ("prod" vs "non-prod"; "service" vs "batch"). Google's published cluster traces follow the same shape — the workload class is metadata supplied by the submitter, not inferred. That is consistent with USPTO 7,493,419 (the input/output workload fingerprinting patent, IBM 2009), which derives its workload class from I/O request *characteristics* (read/write ratio, sequentiality, queue depth) rather than process identity.

The literature relevant to ventd's problem is therefore not the cluster-scheduling literature but the **anomaly-detection-by-process-classification** literature, where techniques like eBPF execsnoop and runtime process clustering are used. The takeaway from that literature is that *process-identity-based fingerprinting works well only when (a) input is denoised against short-lived processes and (b) classification is over a multi-process set, not single-process attribution.* The "denoise against short-lived processes" requirement is the entire reason for the EWMA design in Q4.

### Candidate criteria

| Criterion | Pros | Cons |
|---|---|---|
| CPU% threshold (>5 % of 1 core, EWMA over 10 s) | Captures the *current* workload directly; aligns with what top shows; aligns with R5's idle-gate design | A long-running idle daemon with a brief CPU burst flickers in/out of the signature |
| RSS threshold (>256 MiB) | Captures the *resident* footprint of e.g. a game whose render thread is blocked but is still the workload; captures a paused VM | A single Electron app pushes RSS without driving thermal load, polluting the signature on idle |
| Hybrid (CPU >5 % EITHER OR RSS >256 MiB) | Captures both the active CPU spinner and the heavy-but-paused workload; matches user intuition | Slightly more bookkeeping |
| `cgroups v2 cpu.stat` per-cgroup attribution | Cleanly aggregates worker pools (e.g. one cgroup for chrome, one for systemd-user-session) | cgroup names are user-set and high-cardinality; on systemd-managed desktops they look like `user@1000.service/app-org.kde.konsole-…scope` and are unstable across runs |

### Negative filter: cross-reference with R5's idle-gate blocklist

R5's idle-gate blocklist (rsync, restic, borg, plex-transcoder, jellyfin-ffmpeg, ffmpeg, handbrakecli, x264, x265, make, apt, dpkg, dnf, rpm, pacman, updatedb, plocate-updatedb, smartctl, fio, stress-ng, sysbench) names processes whose presence should not *prevent* the system from being treated as idle for fan-curve purposes. The question for R7 is whether to give those processes the same treatment in the signature library.

The two cross-cutting choices are:

(A) **Blocklist excludes them from contribution** — they would never appear in any signature, and the system's "Plex-transcode workload" would be identified by whichever non-Plex processes happened to be co-running. This is wrong: Plex *is* the workload during a transcode, and ventd's whole purpose is to learn the right curve for it.

(B) **Blocklist promotes them to a dedicated reserved signature class** — i.e., when `plex-transcoder`'s EWMA CPU share dominates, the signature is `reserved/maintenance/plex-transcoder` rather than the SipHash hex. This avoids the "every maintenance task gets its own learned curve" cardinality blowup while still giving Layer C a stable label to attach RLS state to.

**R7 recommends (B)**: maintenance processes get hashed normally for the underlying multiset, but if a maintenance-class hash is the dominant contributor to the K=4 set, the *signature label* used for Layer C state lookup is overridden to `maint/<canonical-name>`, where `<canonical-name>` is one of a fixed list of ~15 reserved tokens (`maint/rsync`, `maint/plex-transcoder`, `maint/ffmpeg`, `maint/make`, `maint/apt`, `maint/dpkg`, etc.). This collapses the long tail of "maintenance + something-tiny" combinations into a small set of reusable buckets and gives Layer C explicitly recognisable categories for its RLS state.

The reserved tokens are *plaintext*, not hashed, but they are also publicly documented constants that leak no machine-specific information — so they do not require salt protection. They are the only plaintext labels in the signature library.

### Recommended top-N criterion

A process P contributes to the signature multiset on tick t with weight w<sub>t</sub>(P) iff:

```
EWMA_cpu(P) > 0.05  OR  RSS(P) > 268_435_456
AND
P.comm is not in the kernel-thread set (no kthreads; comm starts with "[" in ps -ef shorthand,
or P.PPid == 2 in /proc/PID/status)
```

where `EWMA_cpu(P)` is the CPU-share over a 10-second window with α = 0.5 per 2 s tick (half-life ≈ 2 s). The weight contributed each tick is `EWMA_cpu(P)` (so the multiset is naturally CPU-weighted). The signature multiset has its own decay (Q4) and the published signature is the top-K=4 hashes by accumulated weight.

The threshold values 5 % / 256 MiB are deliberately conservative defaults — they are calibrated to exclude the large "idle desktop" tail (systemd, dbus-daemon, NetworkManager, pipewire, …) on every member of the HIL fleet. They should be configurable in `ventd.toml` but are not user-tunable in the same sense as fan curves; they are platform constants that should match what `top` shows when nothing is happening.

---

## Q3. Hash Inputs

### Privacy/stability matrix

| Input | Stability | Privacy surface | Verdict |
|---|---|---|---|
| `comm` | High — kernel-canonicalised, length-bounded, set by the binary itself | Minimal — names like `chrome`, `cc1`, `ffmpeg` are publicly known software names | **include** |
| `exe` symlink target | Low — varies between distros (`/usr/bin/cc1` vs `/usr/libexec/gcc/x86_64-linux-gnu/13/cc1` vs Nix/Guix store paths); for user binaries reveals `/home/$USER/...` | High — leaks home directory paths, project names, Nix profile hashes, AppImage names | exclude |
| `cmdline` first N args | Variable — depends on shell quoting, shell history expansion, makefile variables | Very high — leaks DB passwords passed via `-p`, file paths, URLs, user names | exclude |
| `parent-comm` | Variable — same workload differs depending on whether launched from KDE menu (`plasmashell`), terminal (`bash`), `systemd-run`, Steam (`steam`), or VS Code terminal (`code`) | Low | exclude |

The argument for `parent-comm` deserves a second look because it is the most plausibly-useful of the three rejected candidates: distinguishing `python3` invoked by Jupyter from `python3` invoked by an apt postinst from `python3` invoked by gnome-shell extensions could in principle let Layer C learn three distinct workload curves. But empirically, on every fleet member listed in the HIL matrix, the parent of a long-lived `python3` ranges across `bash`, `systemd`, `code`, `tmux: server`, `jupyter-server`, and `kglobalaccel5` depending on context, and the same workload type produces all of those on different days. Including parent-comm therefore produces *more* false signature flap, not less, for the most common ambiguous case. Dropping parent-comm is the right call.

### Hash input specification

Hash input is exactly:

```
input = comm                              // null-terminator stripped, no padding
hash  = SipHash-2-4(salt[0:16], input)
label = hex.EncodeToString(hash)          // 16 lowercase hex chars
```

Note that ventd does *not* canonicalise `comm` further (no lowercasing, no whitespace stripping). The kernel already enforces 0–15 printable characters, and `chrome` vs `Chrome` vs `chrome ` would all be different upstream binaries with potentially different workload profiles. Bit-identical input to bit-identical output.

### Cross-reference: review-flag against the locked design

The brief's locked design says "Read process names from `/proc/PID/comm` (NOT cmdline)" — this is correct and consistent with what R7 recommends. **No contradiction surfaced.** R7 affirms the locked decision and observes that the cmdline alternative would have created a P9-redactor dependency for *every* signature operation, which would be operationally fragile.

---

## Q4. Stability Under Common False-Flag Scenarios

This is the question that drives the most consequential design decision in R7, and it is where naive top-N-snapshot fails outright.

### Scenario 4.1: Steam game launch

The Valve Proton process model is observable in any `steam_logs/stderr.txt`: as the user clicks "Play", the Steam client emits a continuous stream of `Adding process N for gameID …` and `Removing process N for gameID …` lines. A representative excerpt from a real launch (Linux Mint forum thread on Proton 8 / Steam Deck class hardware, with a small indie game):

```
Adding process 28643 for gameID 3209900    (steam.sh)
Adding process 28644 for gameID 3209900    (reaper)
Adding process 28645 for gameID 3209900    (pressure-vessel-)
Adding process 28646 for gameID 3209900    (pv-bwrap)
Adding process 28759 for gameID 3209900    (wine64-preloader)
Adding process 28760 for gameID 3209900    (services.exe)
Adding process 28761 for gameID 3209900    (winedevice.exe)
Adding process 28762 for gameID 3209900    (plugplay.exe)
Adding process 28765 for gameID 3209900    (svchost.exe)
Adding process 28767 for gameID 3209900    (rpcss.exe)
Adding process 28770 for gameID 3209900    (explorer.exe)
Adding process 28780 for gameID 3209900    (steamwebhelper)
Adding process 28835 for gameID 3209900    (Game.exe)
…
Removing process 28835 for gameID …       (transient launcher exit)
Removing process 28780 for gameID …
…
```

The pattern is clear: roughly 10–20 distinct executables transit through TASK_RUNNING during the first ~5 seconds of a game launch, with most exiting within 1–3 seconds (services.exe, plugplay.exe, rpcss.exe are setup-only). The Game.exe itself and dxvk shader-cache-build threads (typically a process named `vkd3d-proton-* ` or `dxvk_compile`) then dominate steady-state.

**Naive top-N snapshot every 1 s:**
- t = 0 s:  signature = {steam, plasmashell, kded5, dbus-daemon} (idle-desktop)
- t = 1 s:  signature = {steam, reaper, pressure-vessel-, pv-bwrap}
- t = 2 s:  signature = {wine64-preloader, services.exe, plugplay.exe, svchost.exe}
- t = 3 s:  signature = {explorer.exe, steamwebhelper, Game.exe, dxvk_compile}
- t = 5 s:  signature = {Game.exe, dxvk_compile, GPU-thread, audio-thread}
- t = 30 s: signature = {Game.exe, GPU-thread, …}

That is at least *four* distinct hash buckets created in five seconds. Each bucket is a freshly-allocated RLS estimator with default priors; Layer C learns *nothing* during the launch and may even mis-train. The signature does not stabilise until the game's render loop has run long enough to dominate the EWMA — which on naive top-N never happens because top-N ignores history.

**EWMA-multiset behaviour (recommended design):**
At t = 0, the multiset is full of desktop-idle hashes. Over t = 1…5 the launch processes inject weight, but each one decays at α per tick; only Game.exe and the GPU-thread accumulate enough weight to displace the desktop-idle entries. By t ≈ 6 s the top-K=4 stabilises on {Game.exe, GPU-thread, plasmashell, pipewire-pulse}, and the K-stable-promotion gate (≥3 consecutive ticks of identical top-K) fires at t ≈ 12 s. Layer C looks up the (channel, signature) RLS state once at t ≈ 12 s and trains continuously thereafter.

### Scenario 4.2: gcc / cc1 / ld toolchain churn during `make -j$(nproc)`

A kernel build with `make -j16` on a 13900K spawns roughly 200 cc1 processes per second during the C-compile phase, each lasting 0.3–1.5 seconds, plus a smaller stream of `as` (assembler), `ld` (linker, 1–10 s lifetime), `objtool`, `objcopy`, and `genksyms` invocations. The make process itself is a long-lived parent. Per the kernel's `Documentation/kbuild/llvm.html`, the call chain on a Clang build is `make → clang → cc1 → as`; on the GCC build it is `make → gcc → cc1 → as` then `gcc → collect2 → ld`. Each `cc1` is a fork from the gcc driver, lives for the duration of the source file's compilation, and exits.

Because the kernel build is highly parallel, `ps` would show several cc1 processes simultaneously at any sample point, but *which* cc1 PIDs are present changes every sample. Their `comm` is always `"cc1"`, however — and that is the saving observation.

**The signature is by `comm`, not by PID.** All concurrent cc1 processes hash to the same bucket. The CPU-weight contributed to that bucket each tick is the *sum* of CPU shares of all live cc1 processes, which during a build is consistently 80–95 % of total system CPU. The signature converges within one EWMA half-life (~2 s) on `{cc1, make, ld, as}`. This is one of the most stable workloads in the entire fleet — counter-intuitively, the kernel build is *easier* for ventd to fingerprint than e.g. web browsing.

### Scenario 4.3: Browser tab / process churn under Chrome Site Isolation

Chrome's Site Isolation, fully enabled by default since Chrome 67 on desktop and 77 on Android with ≥2 GB RAM (per the Chromium `process_model_and_site_isolation.md` document), assigns *one renderer process per site*, plus separate processes for cross-site iframes (out-of-process iframes, OOPIFs). Critically, Site Isolation is *site-per-process*, not *origin-per-process*: pages from `mail.google.com` and `docs.google.com` share a process because they share the eTLD+1.

For a typical user with 20 tabs across 8 distinct sites, the Chrome process tree is roughly:
- 1 browser process (`chrome` or `chromium`)
- 1 GPU process
- 1 network process
- 1 utility process (audio, storage)
- 8 renderer processes (one per site; a high-volume single-site user could have one 16-tab window in one renderer)
- Variable: 0–N OOPIF renderers for cross-site embeds (ad iframes, embedded YouTube, etc.)

**The comm for every renderer is the same:** `chrome` (or, for Chromium-derived browsers, the binary's basename). They differ in `cmdline` (`--type=renderer`, etc.) but not in comm. So as far as ventd is concerned, all of Chrome — every renderer, the GPU process, the network process — collapses to a single hash bucket. Tab churn is invisible. OOPIF spawning is invisible. Closing a tab and opening a new one on a different site changes nothing in the signature.

**Caveat:** Firefox-based browsers (Project Fission rolled out in 2021 per the Wikipedia *Site isolation* article) use distinct comm names per process type: `firefox`, `firefox-bin`, plus per-process-type comm tweaks like `Web Content`, `WebExtensions`, `RDD Process`, `Socket Process`. That means a Firefox-driven browsing workload spreads weight across 3–5 hash buckets rather than 1. The top-K=4 design accommodates this directly: Firefox's signature is `{firefox, Web Content, WebExtensions, RDD Process}`, stable across tab churn.

This is the dominant case where the EWMA design is *unnecessary* — both browsers are well-behaved by comm — but it is also the case where users most expect stability, so the design choice is consistent.

### Scenario 4.4: systemd-resolved cycling — REVIEW FLAG

**The brief asks "does it cycle? if so, how often?" The honest answer is: in normal operation, it does not cycle.** The systemd-resolved daemon is a long-lived stub resolver. The Debian `systemd-resolved.service(8)` manpage and the Ubuntu manpage describe its restart triggers as: SIGUSR1 (dump caches), SIGUSR2 (flush caches), SIGRTMIN+1 (re-probe DNS server features), SIGHUP (reload config). None of these *terminate* the process. The daemon is started by `systemd-resolved.service` with `Restart=on-failure` (per the upstream unit file) and restarts only on crash.

The user-visible bug reports of systemd-resolved "stopping" (e.g. `systemd/systemd#21123`) are *failure-to-resolve* bugs in which the daemon stops servicing queries but the process is still running — the daemon does not exit and respawn. Searches for "systemd-resolved cycling" return references to feature-level *learning* cycles internally, not process restart cycles.

**Conclusion:** systemd-resolved is *not* a churn source under the EWMA model. It is a long-lived background process whose CPU contribution is essentially zero (well below the 5 % gate) and whose RSS is on the order of 10–40 MiB (well below the 256 MiB gate). It will never appear in any signature on any of the fleet members. **The brief's prior assumption that systemd-resolved cycles is, on review, unsupported.** This is surfaced as a review flag in Part 2.

A *related* but distinct phenomenon does exist and is worth naming: short-lived `systemd-resolved`-adjacent processes such as `systemd-resolve` (the legacy command-line tool) and DBus-spawned NSS workers can flicker in and out, but they do not pass either gate.

### Stabilisation mechanism: EWMA-weighted hash multiset with K-stable promotion

```
state per signature library:
    mset:   map[hash]float64    // EWMA accumulated weight
    label:  string              // currently-promoted signature label
    pending_label:   string     // proposed next label
    pending_streak:  int        // consecutive ticks pending_label has been the top-K

per tick (default 2 s, matched to R11 fast-loop):
    1. decay: for h in mset: mset[h] *= alpha
       where alpha = 0.5^(tick_dt / half_life), half_life = 2 s
    2. for each process P passing the contribution gate:
         h := SipHashKeyed(salt, P.comm)
         mset[h] += EWMA_cpu(P)        // CPU-share over last 10 s
    3. drop entries where mset[h] < epsilon = 1e-3
    4. compute top_k := top K=4 hashes by mset weight
       canonicalise: sort top_k lexicographically, join with '|', truncate to 80 chars
    5. if top_k == pending_label:
         pending_streak += 1
         if pending_streak >= M=3 (≥6 s of stability):
            label := pending_label
            pending_streak := 0
       else:
         pending_label := top_k
         pending_streak := 1
```

The design has three knobs:
- **half-life** (2 s default): controls how quickly the multiset forgets transient processes. Aligns with R11's fast-loop tick.
- **K = 4** (top-K size): controls how many parallel processes contribute identity. K=4 captures the dominant-binary + 1–3 supporting-binaries pattern observed across all four scenarios.
- **M = 3** (stability gate): controls how long the top-K must be stable before Layer C state lookup. M = 3 ticks at 2 s each = 6 seconds of stability required, which is short enough to pick up real transitions (game launch → game steady state) and long enough to filter compile-storm reshuffles.

These values are spec-time defaults; they should be configurable in `ventd.toml` under `[smart.signature]` but are not exposed in the per-channel curve config (which would be the wrong abstraction).

### Prior art

The EWMA-weighted multiset is the canonical pattern for online process clustering and is documented in:

- **eBPF-based observability tools** (e.g. `bcc` `execsnoop`, `iovisor/bpftrace`): these tools count process-spawn events but are explicitly *event-driven* rather than top-N-driven. The R7 design takes the analogous "smoothed history" approach to a polling source.
- **Borg's "equivalence classes"** (Verma et al., EuroSys 2015, §3.4): Borg groups tasks by *identical resource requirements* and uses class-level scheduling decisions. ventd's K-stable label is the analogous "decisions are made over the equivalence class, not the individual task."
- **USPTO 7,493,419** (IBM I/O workload fingerprinting, 2009): classifies I/O workloads by *characteristics*, then maintains a *list* of classifications and adjusts scheduler tunables based on them. This is the closest published analogue to Layer C's plan: classify, look up, adjust.

---

## Q5. Bucket Count, LRU Eviction, Persistence

### Distinct-signature working-set size

The realistic universe of distinct workload signatures on the fleet is bounded by the realistic universe of distinct *top-K=4 ordered tuples of hashes*. Because the 5 % / 256 MiB gates exclude the long tail of always-running daemons, the dominant tuples are driven by what the user actually does — and on a homelab/desktop user, that set is empirically small.

A representative breakdown for the Proxmox host (5800X+3060) over a hypothetical year:

| Workload class | Likely top-K | Signatures expected |
|---|---|---|
| Idle desktop / IPMI watch | {plasmashell, pipewire-pulse, kded5, dbus-daemon} | 1 |
| Web browsing (Chrome) | {chrome, plasmashell, pipewire-pulse, Xorg} | 1 |
| Web browsing (Firefox) | {firefox, Web Content, WebExtensions, RDD Process} | 1 |
| Video playback (mpv) | {mpv, pipewire-pulse, plasmashell, Xorg} | 1 |
| Video playback (vlc) | {vlc, pipewire-pulse, plasmashell, Xorg} | 1 |
| YouTube in browser | (collapses into Chrome/Firefox) | 0 (covered) |
| Compile (kernel) | {cc1, make, ld, as} | 1 |
| Compile (Rust) | {rustc, cargo, ld, …} | 1 |
| Compile (Go) | {go, compile, link, vet} | 1 |
| Steam game (per game) | {Game.exe, dxvk-cache, plasmashell, pipewire-pulse} | ≈ 5–15 (one per actively-played title) |
| Plex transcode | maint/plex-transcoder | 1 (reserved) |
| Jellyfin transcode | maint/jellyfin-ffmpeg | 1 (reserved) |
| ZFS scrub | maint/zfs (reserved) | 1 |
| Backups | maint/restic, maint/rsync, maint/borg | 3 (reserved) |
| Package update | maint/apt, maint/dpkg, maint/dnf | 3 (reserved) |
| ML inference (local) | {ollama, ollama runner, … } | 2–4 |
| VM running | {qemu-system-x86, …} | 1–3 (one per VM type) |

That puts a hot-fleet user at ~25–40 distinct signatures in active use, with a long tail of one-off launches (a single random game played once) bringing the lifetime total to maybe 60–90 over a year. **128 buckets is the right default**: it provides headroom of ~2× over the realistic working set, fits comfortably in the spec-16 KV budget (each bucket is on the order of ~200 bytes serialised = 25.6 KiB total), and means LRU eviction is essentially never triggered in practice.

### LRU eviction

Pure last-seen LRU has a known failure mode here: a frequently-used signature (e.g. a daily compile workload) that the user happens to not hit for two weeks while on holiday gets evicted by transient one-off games. Pure recency is the wrong order. **Recommend weighted-LRU with eviction score:**

```
score(bucket) = hit_count * exp(-(now - last_seen_ts) / tau)
where tau = 14 days (1.21e6 seconds)
```

When the library is full and a new signature must be inserted, evict the bucket with the *lowest* score. This preserves "well-loved but recently dormant" signatures over "one-off" ones. Tau = 14 days is set deliberately to span typical "I went on holiday" gaps and shorter than typical "I switched to a different machine" gaps.

### Persistence: spec-16 KV shape

The signature library uses spec-16's **KV** storage shape (not blob, not append-only log). KV is correct because:
- The library has many small entries (~200 bytes each) with random-access read on label lookup.
- Updates are read-modify-write per entry, not append.
- Append-only log would conflate signature creation with signature update; blob would force whole-library rewrites on every tick.

Schema:

```
key:   "workload-sig/<16-hex-or-canonicalised-tuple>"
       e.g. "workload-sig/3a9f2c01b8e74def"
       e.g. "workload-sig/3a9f|9b22|ce04|f5a1"  (top-K=4 tuple, sorted, '|' separator)

value: msgpack-encoded struct {
    Version          uint8     // schema version, currently 1
    HashAlg          uint8     // 1 = SipHash-2-4
    LabelKind        uint8     // 0 = hash-tuple, 1 = reserved (maint/...)
    RLSState         []byte    // opaque, owned by Layer C estimator
    FirstSeenUnix    int64
    LastSeenUnix     int64
    HitCount         uint64
    CurrentEWMA      float64   // saved multiset weight for warm restart
    SchemaReserved   []byte    // future fields
}
```

On daemon start: enumerate all `workload-sig/*` keys; deserialise into in-memory map; rebuild the EWMA multiset from `CurrentEWMA` of the most recently active bucket. The fast loop's first tick after startup begins decay-multiplying these values immediately, so within a few half-lives the live multiset has fully replaced the persisted state — exactly the warm-restart behaviour you want.

The salt file (`/var/lib/ventd/.signature_salt`) is *not* in the KV store; it lives at a fixed path with mode 0600 to keep it out of any state-export operation that might walk the KV. This is intentional: the KV may be exported for diag bundles (with the per-key opacity discussed in Q6), but the salt must never be exported even in privileged operator dumps.

---

## Q6. Privacy

### Per-install salt

```
path:        /var/lib/ventd/.signature_salt
length:      32 bytes
mode:        0600
owner:       ventd:ventd
generation:  on first daemon start, atomic create-tmp-rename
content:     output of crypto/rand.Read(32)
exclusion:   not in diag bundles (P9 redactor enforcement),
             not in logs,
             not in metrics export,
             not in --reveal-signatures dumps
```

If the file is missing on startup, ventd generates a fresh salt and proceeds. **Generating a fresh salt invalidates every existing signature in the KV** (the keys no longer correspond to recoverable comm names) — but the RLS state is still retained, just under stale labels that will be LRU-evicted naturally as new traffic arrives. The salt rotation is a privacy-recovery operation; an operator can force it with `ventd ctl rotate-salt` (which does *not* preserve any cross-rotation correlation) or `ventd ctl rotate-salt --reseed-buckets` (which discards all RLS state and starts learning from scratch — preferable when the operator suspects salt compromise).

### HMAC vs keyed SipHash — construction comparison

Standard HMAC-SHA-256 over a 16-byte input:

```
HMAC(K, m) = SHA256(K' XOR opad ‖ SHA256(K' XOR ipad ‖ m))
```

This is two SHA-256 calls, each over a 64-byte block (after padding), producing a 256-bit tag. RFC 2104 proves it is a secure MAC under the assumption that the underlying compression function is a PRF.

Keyed SipHash-2-4 over the same 16-byte input is a *single*, direct PRF evaluation: 4 ARX rounds on input absorption plus 4 finalisation rounds, producing a 64-bit tag. It is strictly faster for short inputs (in the ~10 ns range vs ~150–300 ns for HMAC-SHA-256, both pure Go).

The security argument is comparable for this application:

- **HMAC-SHA-256:** standard, audited, FIPS-blessed, but optimised for variable-length high-entropy inputs (TLS records, JWT bodies).
- **Keyed SipHash-2-4:** designed specifically for short-input keyed PRF use; published cryptanalysis bounds key-recovery effort at >2^128 work; explicitly designed for the threat model "attacker sees many `(message, MAC)` pairs of attacker-chosen short messages and cannot recover the key" — which is exactly the leaked-diag-bundle threat.

For ventd's needs (16-byte input, anti-rainbow-table privacy, fast hot path, pure Go, simple code surface) **SipHash is strictly better than HMAC-SHA-256.** The brief explicitly invites this comparison and the answer is: SipHash-2-4 with the salt as key is not just *equivalent* to HMAC-SHA256(salt, comm), it is *better suited* to this exact problem.

The one place HMAC would win is FIPS-mode compliance (SHA-256 is a FIPS-blessed primitive; SipHash is not). ventd does not currently target FIPS mode; if a future deployment required it, swapping to HMAC-SHA-256-truncated-to-64-bits is a one-file change behind the existing `Hasher` interface.

### Operator escape hatch: `ventd doctor --reveal-signatures`

Behaviour:

```
$ sudo ventd doctor --reveal-signatures
ventd doctor: --reveal-signatures requires the controller to be paused.
  Run: sudo ventd ctl pause --reason="signature debug"
  Then re-run this command.
  The pause will be auto-released after 10 minutes.

$ sudo ventd ctl pause --reason="signature debug"
ventd: controller paused (reason: signature debug, auto-release: 10m).

$ sudo ventd doctor --reveal-signatures
WARNING: this dump contains plaintext process names from the running fleet.
         Do NOT redirect into a file you intend to share.
         The controller is paused; fan curves are at refusal-safe defaults.

Currently active signatures (top 8 by EWMA weight):

  3a9f2c01b8e74def  weight=0.42  (chrome)
                                  (chrome)
                                  (chrome)
                                  (Xorg)
  9b22ed8f1a4c0712  weight=0.31  (cc1)
                                  (cc1)
                                  (make)
                                  (ld)
  …

Library contents (128 buckets, 47 in use):

  3a9f2c01b8e74def  hits=2389  last_seen=2 s ago    label=hash-tuple
  9b22ed8f1a4c0712  hits=1102  last_seen=4 m ago    label=hash-tuple
  maint/plex-transcoder  hits=88  last_seen=6 h ago label=reserved
  …

Salt file: /var/lib/ventd/.signature_salt (NOT shown).
```

Implementation rules:
- The dump is to stdout, *never* to a log file or diag bundle.
- The dump never includes the salt.
- The mapping is reconstructed on the fly by hashing every currently-running process's comm and joining; the live mapping is *not* persisted to disk and is freed when the command exits.
- The pause-controller acknowledgement is enforced because the dump's diagnostic value is highest when the operator is also running fan curves manually for comparison; coupling the two prevents the misuse case "leave reveal-mode on as a logging convenience."
- A no-pause `--force` flag is *not* offered. There is no legitimate use case for it.

### Diag-bundle behaviour

Default: opaque hash IDs only. The KV is dumped as `workload-sig/<hex>` keys with msgpack values; the values contain RLS state and counters but no comm-name reverse mapping.

With `ventd diag --include-signatures`: same as above. The salt is *still excluded*. A leaked bundle hash is still not reversible by an attacker without obtaining the salt out-of-band from the user's machine — i.e. the bundle is privacy-safe to share even with the flag set.

With `ventd diag --include-signatures --include-salt`: the salt is included. This flag exists *only* for the case "user explicitly wants to share a bundle that another instance of ventd can replay deterministically" — which is a developer/CI scenario, not a normal user scenario. The flag prints a 5-line warning, requires `--i-understand-this-leaks-process-names`, and is documented as "supplies the cryptographic key that protects all other signature hashes in this bundle. Do not share with parties you do not fully trust." This is the same severity as `--include-credentials` would have if such a flag existed.

Cross-reference with R5: R5's idle-gate blocklist is *static plaintext* in the spec and binary, not a privacy concern. R7's reserved labels (`maint/<canonical-name>`) reuse R5's blocklist as the canonical-name dictionary — they are explicitly safe to ship in plaintext in the diag bundle because they are derived from the public spec, not from the user's machine.

---

## Cross-References

### R5 idle-gate process blocklist

The R5 list is reused by R7 as the **reserved-label dictionary** for the maintenance-class signature. R7 does *not* duplicate the list; the canonical source is `internal/idle/blocklist.go` (R5's home), and `internal/signature/blocklist.go` is a thin shim that re-exports R5's list via a stable interface. This avoids version skew. If R5's list expands to add (say) `maint/zstd` for compressed-backup workloads, R7 inherits it for free.

### R11 Layer C saturation thresholds

R7's EWMA half-life (2 s) is set to match R11's fast-loop tick (also 2 s, per the brief). The K-stable promotion threshold (M = 3 ticks = 6 s) is short enough to react to genuine workload transitions before R11's slow-loop window (3 min) makes a Layer C decision. Mathematically: by the time Layer C's slow loop evaluates a 3-minute saturation window, the signature has been promoted for at least 174 of those 180 seconds (97 %). Layer C's RLS state is therefore overwhelmingly attributable to the current label, not to the transition.

### spec-16 persistent state

The signature library uses spec-16's **KV** storage shape exclusively. It does *not* use blob (would force whole-library rewrites) or append-only log (would conflate signature creation events with state updates). The salt file is *outside* spec-16's domain — it lives at a fixed filesystem path under `/var/lib/ventd/` with bespoke permissions — because spec-16's transactional/atomic-rename machinery is unnecessary overhead for a 32-byte file written exactly once at install time.

### R1 Tier-2 detection (containers / VMs)

R7 inherits R1's refusal classes wholesale: when ventd is running inside a container (Tier-2 BLOCK) or on a VM detected as a constrained guest, **signature learning is disabled** and Layer C operates on the coarse-classification fallback only. Rationale: in a container, `/proc/PID/comm` reflects the *host* process namespace if the container has `--pid=host`, or the *container* namespace if not, and either way the classification has no operational meaning for the host's fan curves (which is what ventd is controlling). The signature library is read-only in those modes — it can serve previously-learned labels but does not update.

### R3 hardware_refusal class

R7 inherits R3's hardware-refusal: on Steam Deck and any future hardware-refused platform, signature learning is disabled. The signature library is not even instantiated; the package's `Init()` is a no-op when `R3.HardwareRefused()` returns true.

---

## Recommended Design — Pseudocode

```go
// internal/signature/library.go
package signature

import (
    "math"
    "sort"
    "strings"
    "sync"
    "time"
)

const (
    DefaultBucketCount   = 128
    DefaultK             = 4
    DefaultStabilityM    = 3
    DefaultHalfLifeSec   = 2.0
    DefaultLRUTauSec     = 14 * 24 * 3600.0 // 14 days
    DefaultDropEpsilon   = 1e-3
    DefaultCPUGate       = 0.05  // 5% of one core, EWMA over 10 s
    DefaultRSSGateBytes  = 256 * 1024 * 1024
)

type ProcessSample struct {
    Comm        string  // /proc/PID/comm, null-stripped
    EWMACPU     float64 // share of one core
    RSSBytes    uint64
    PPid        int
    IsKThread   bool
}

type Library struct {
    mu          sync.Mutex
    hasher      *Hasher
    blocklist   *Blocklist          // shared with R5
    multiset    map[uint64]float64  // hash -> EWMA weight
    buckets     map[string]*Bucket  // label -> bucket (rls state, counters)
    pending     string
    pendingHits int
    label       string
    lastTick    time.Time
    cfg         Config
}

type Bucket struct {
    Label         string
    RLSState      []byte
    FirstSeenUnix int64
    LastSeenUnix  int64
    HitCount      uint64
    CurrentEWMA   float64
}

type Config struct {
    BucketCount       int
    K                 int
    StabilityM        int
    HalfLifeSec       float64
    LRUTauSec         float64
    CPUGate           float64
    RSSGateBytes      uint64
    Disabled          bool // R1/R3 inheritance
}

// Tick is called by smart-mode at fixed cadence (2 s, matched to R11 fast loop).
// processes is the snapshot of /proc walks; samples below the gate are filtered
// by the caller (ventd/internal/proc) but Tick re-applies for safety.
func (lib *Library) Tick(now time.Time, processes []ProcessSample) (label string, promoted bool) {
    lib.mu.Lock()
    defer lib.mu.Unlock()

    if lib.cfg.Disabled {
        return "fallback/disabled", false
    }

    // (1) decay the multiset
    var dt float64
    if !lib.lastTick.IsZero() {
        dt = now.Sub(lib.lastTick).Seconds()
    } else {
        dt = lib.cfg.HalfLifeSec
    }
    alpha := math.Pow(0.5, dt/lib.cfg.HalfLifeSec)
    for h, w := range lib.multiset {
        nw := w * alpha
        if nw < lib.cfg.DropEpsilon() {
            delete(lib.multiset, h)
        } else {
            lib.multiset[h] = nw
        }
    }
    lib.lastTick = now

    // (2) inject this tick's contributions
    for _, p := range processes {
        if p.IsKThread || p.PPid == 2 {
            continue
        }
        if p.EWMACPU <= lib.cfg.CPUGate && p.RSSBytes <= lib.cfg.RSSGateBytes {
            continue
        }
        // R5 blocklist: maintenance-class processes are still hashed,
        // but if they end up dominant, the label is overridden to maint/.
        h := lib.hasher.HashComm(p.Comm)
        lib.multiset[h] += p.EWMACPU
    }

    // (3) extract top-K
    type entry struct {
        h uint64
        w float64
    }
    entries := make([]entry, 0, len(lib.multiset))
    for h, w := range lib.multiset {
        entries = append(entries, entry{h, w})
    }
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].w > entries[j].w
    })
    k := lib.cfg.K
    if len(entries) < k {
        k = len(entries)
    }
    if k == 0 {
        // System truly idle; emit dedicated label.
        return lib.commit("idle", false), false
    }
    topK := entries[:k]

    // (3a) maintenance-class override
    if maint := lib.detectMaintDominant(processes, topK); maint != "" {
        return lib.commit(maint, true), lib.commit(maint, true) != lib.label
    }

    // (3b) canonicalised hash-tuple label
    parts := make([]string, k)
    for i, e := range topK {
        parts[i] = encodeHash16(e.h)
    }
    sort.Strings(parts)
    candidate := strings.Join(parts, "|")
    if len(candidate) > 80 {
        candidate = candidate[:80]
    }

    // (4) K-stable promotion gate
    if candidate == lib.pending {
        lib.pendingHits++
        if lib.pendingHits >= lib.cfg.StabilityM && lib.pending != lib.label {
            lib.label = lib.pending
            lib.pendingHits = 0
            return lib.label, true
        }
    } else {
        lib.pending = candidate
        lib.pendingHits = 1
    }
    return lib.label, false
}
```

```go
// internal/signature/persistence.go (sketch)
package signature

import (
    "github.com/ventd/internal/store"   // spec-16 KV adapter
    "github.com/vmihailenco/msgpack/v5" // already a project dep per spec-16
)

const KVPrefix = "workload-sig/"

func (lib *Library) Save(kv store.KV) error {
    for label, b := range lib.buckets {
        key := KVPrefix + label
        v, err := msgpack.Marshal(b)
        if err != nil {
            return err
        }
        if err := kv.Put(key, v); err != nil {
            return err
        }
    }
    return nil
}

func (lib *Library) Load(kv store.KV) error {
    keys, err := kv.PrefixScan(KVPrefix)
    if err != nil {
        return err
    }
    for _, key := range keys {
        v, err := kv.Get(key)
        if err != nil {
            continue
        }
        var b Bucket
        if err := msgpack.Unmarshal(v, &b); err != nil {
            continue // skip corrupt buckets, do not abort startup
        }
        b.Label = strings.TrimPrefix(key, KVPrefix)
        lib.buckets[b.Label] = &b
        // warm-restart EWMA: only the last-active bucket's CurrentEWMA
        // is restored to lib.multiset; older buckets contribute hit_count
        // and last_seen for LRU but not live weight.
    }
    return nil
}
```

---

# PART 2 — Spec-Ready Findings Appendix Block

## R7 — Workload signature hash for Layer C of smart-mode

**Defensible default(s):**

- **Hash function:** SipHash-2-4 (`github.com/dchest/siphash`, public-domain, pure-Go), keyed with the per-install salt. Output 64 bits, rendered as 16 lowercase hex chars.
- **Hash input:** `/proc/PID/comm` only (16-byte kernel-truncated). Not exe, not cmdline, not parent-comm.
- **Salt:** 32 bytes from `crypto/rand`, stored at `/var/lib/ventd/.signature_salt`, mode 0600, owner ventd:ventd, never logged, never in diag bundles.
- **Top-N gate:** EWMA-CPU-share > 5 % of one core OR RSS > 256 MiB, EWMA window 10 s, kthread filter (PPid != 2).
- **Multiset model:** EWMA-weighted hash multiset, half-life 2 s (= R11 fast-loop tick), drop epsilon 1e-3.
- **Signature label:** top-K=4 by weight, sorted lexicographically, '|'-joined, max 80 chars. Maintenance-class override produces `maint/<canonical-name>` from R5's blocklist.
- **Stability gate:** K-stable for M=3 consecutive ticks (= 6 s) before promotion to active label.
- **Library size:** 128 buckets, weighted-LRU eviction with τ = 14 days.
- **Persistence:** spec-16 **KV** shape, key `workload-sig/<label>`, msgpack-encoded value containing `{Version, HashAlg, LabelKind, RLSState, FirstSeenUnix, LastSeenUnix, HitCount, CurrentEWMA, SchemaReserved}`.
- **Reveal escape hatch:** `ventd doctor --reveal-signatures` requires `ventd ctl pause` first, dumps to stdout only, never persists.

**Citation(s):**

- `https://man7.org/linux/man-pages/man5/proc_pid_comm.5.html` — kernel canonicalisation of comm at TASK_COMM_LEN = 16
- `https://www.kernel.org/doc/Documentation/filesystems/proc.txt` — `/proc/[pid]/comm` semantics
- `https://en.wikipedia.org/wiki/SipHash` — SipHash threat model and security claim
- `https://github.com/dchest/siphash` — recommended Go implementation, public-domain
- `https://pkg.go.dev/hash/maphash` and `https://go.dev/src/hash/maphash/maphash.go` — Go runtime SipHash-family hash, with documented per-process Seed limitation
- `https://github.com/zeebo/blake3` — pure-Go BLAKE3 with Plan9 asm, CGO-free
- `https://github.com/cespare/xxhash` — pure-Go xxHash with `purego` build tag
- `https://datatracker.ietf.org/doc/html/rfc2104` — HMAC construction (alternative considered)
- `https://www.chromium.org/developers/design-documents/site-isolation/` — Chrome process model
- `https://chromium.googlesource.com/chromium/src/+/main/docs/process_model_and_site_isolation.md` — site-per-process behaviour
- `https://en.wikipedia.org/wiki/Site_isolation` — Firefox Project Fission and per-process comm differences
- `https://github.com/ValveSoftware/Proton` — Steam/Proton process model
- `https://github.com/ValveSoftware/steam-for-linux` — empirical "Adding/Removing process N for gameID …" telemetry
- `https://docs.kernel.org/kbuild/llvm.html` — kernel build cc1/as/ld toolchain call chain
- `https://manpages.debian.org/unstable/systemd-resolved/systemd-resolved.service.8.en.html` — systemd-resolved restart behaviour (does NOT cycle in normal operation)
- `https://research.google.com/pubs/archive/43438.pdf` — Verma et al., *Large-scale cluster management at Google with Borg* (EuroSys 2015)
- USPTO 7,493,419 — *Input/output workload fingerprinting for input/output schedulers*

**Reasoning summary:**

The choice of SipHash-2-4 over SHA-256/HMAC-SHA-256, BLAKE3, and xxh3 is driven by an exact match between SipHash's design threat model (an adversary observes attacker-known short inputs and their PRF outputs and tries to recover the key or extend the table) and ventd's privacy requirement (a leaked diag bundle exposes hashes of well-known process names; the salt must remain unrecoverable). The locked-design constraint that `comm` is the input source happens to align with SipHash's optimised input-size sweet spot (≤16 bytes), and the `dchest/siphash` implementation is small enough to audit in one sitting. The EWMA-multiset stabilisation design is necessary because three of the four named flap scenarios (Steam launch, gcc/cc1/ld churn, Chrome tab churn under Site Isolation) demonstrably defeat naive top-N snapshots; the kernel-build case is *helped* by hashing on `comm` because all concurrent cc1 processes alias to the same bucket, and the Chrome case is similarly absorbed because all renderer processes share comm. The systemd-resolved scenario is, on review, not a real flap source — the daemon does not cycle in normal operation; this is surfaced as a review flag below. Persistence in spec-16 KV shape is correct because the access pattern is keyed-random read + read-modify-write, not append-only events. The 128-bucket library default is calibrated against the realistic per-user workload taxonomy (~25–40 active, ~60–90 lifetime); 128 leaves comfortable headroom. The privacy escape hatch (`--reveal-signatures`) is gated on `ctl pause` to ensure the controller is in a safe state during debug, and never writes the salt or the live mapping to disk.

**HIL-validation flag:**

- **Proxmox host (5800X+3060):** validates compile workload (kernel build, Rust build), VM workload (qemu-system-x86 signature), ZFS scrub maintenance class, and idle-desktop signature stability. Highest-confidence platform.
- **MiniPC Celeron:** validates the EWMA gate's lower-bound calibration — the 5 %-of-one-core threshold should *not* trigger on background daemons even on the slowest fleet member. If it does, the gate moves up. Also validates LRU eviction under low-cardinality workloads.
- **13900K + RTX 4090 dual-boot desktop:** validates the Steam launch scenario end-to-end (Proton process churn, dxvk shader compile, steady-state game), validates Chrome Site-Isolation (16-tab session, tab churn), validates Firefox Project Fission distinct comm names, validates `maint/plex-transcoder` reserved label.
- **3 laptops:** validate that the EWMA half-life (2 s) does not produce excessive label churn during normal user activity (typing, scrolling, suspend/resume edge cases). Important: at least one laptop must validate cold-boot warm-restart behaviour (label resumes correctly from KV after suspend-to-disk).
- **Steam Deck:** **excluded per R3** — signature learning is disabled in `R3.HardwareRefused()` mode. HIL on Deck only validates the *disabled* path (signature library never instantiates).
- **TerraMaster F2-210 NAS (limited):** validates the spec-16 KV adapter under flash-storage write constraints; validates eviction under the realistic NAS workload (Plex transcode + ZFS scrub + Samba). Cannot validate desktop scenarios.

**HIL gap surfaced:** None of the fleet members run a *Bazel* or *Buck2* build (which produce a different toolchain comm distribution from gcc/clang Make builds); if a future user reports compile-class signature instability on Bazel, R7 may need to add `maint/bazel` and `maint/buck2` to the reserved-label set. This is a low-priority forward-looking note, not a blocker.

**Confidence: Medium-High.**

- High confidence on hash choice (SipHash-2-4 is unambiguously the right primitive for this exact threat model, multiply confirmed in the cryptographic literature).
- High confidence on input choice (comm only, with parent-comm/cmdline/exe rejected for reasons of stability and privacy that are obvious once stated).
- High confidence on the EWMA-multiset stabilisation design for Steam, gcc, and Chrome (these are well-understood process models).
- Medium confidence on the K=4, M=3, half-life=2 s knob values — these are defensible defaults but will likely require empirical re-tuning on the fleet during HIL. Recommend exposing them in `ventd.toml` at v1.0 and revisiting after one quarter of fleet telemetry.
- Medium confidence on bucket count = 128 — could plausibly be 64 (still sufficient) or 256 (still cheap) without significant change to behaviour. 128 is the conservative midpoint.
- High confidence on persistence shape (KV is the only correct choice).
- High confidence on privacy construction (salt-as-key SipHash with a fixed-path salt file is the standard pattern for exactly this problem).

**Spec ingestion target:**

- `spec-smart-mode.md §6.6` (existing): replace the placeholder "signature is computed from /proc/PID/comm of top-N processes, hashed locally" paragraph with a reference to `spec-smart-mode.md §6.6.1` (new sub-section).
- `spec-smart-mode.md §6.6.1` (new): full R7 design, mirrors Part 1 of this document.
- `spec-16.md §<persistence_consumers>` (existing): add `internal/signature` to the list of consumers using KV shape.
- `spec-doctor.md §<reveal_flags>` (existing or new): document `--reveal-signatures` flag and pause-required precondition.
- `spec-privacy.md §<salts_and_keys>` (existing or new): document the per-install salt at `/var/lib/ventd/.signature_salt` and its exclusion from diag bundles by the P9 redactor.

**Review flags:**

1. **systemd-resolved does not cycle.** The brief lists systemd-resolved cycling among the false-flag scenarios, but the systemd-resolved manpages and source make clear that the daemon is long-lived and does not respawn its process. The "cycling" referenced in the brief appears to be either (a) the daemon's *internal* DNS-feature-level probing (which is a state machine within the long-lived process, not process churn) or (b) a misremembered description of e.g. `systemd-timesyncd` behaviour. R7 treats systemd-resolved as a non-issue in the EWMA design and recommends the spec text either drop the reference or rephrase it to describe actual short-lived helpers (`systemd-resolve` CLI tool, NSS workers).

2. **Brief's "top-N snapshot" framing is contradicted by all four scenarios.** The brief notes that "top-N snapshot at 1 Hz will flap badly under all four scenarios" and approves the EWMA-multiset alternative — R7 confirms this is correct, and recommends the spec text *not* describe top-N as the implied default at all. The default should be EWMA-multiset; top-N snapshot is not a fallback or alternative, it is simply wrong for this problem.

3. **Tick rate vs sample rate.** The brief mentions "1 Hz × ~N processes" in the Q1 constraints but R11's fast loop is 2 s (0.5 Hz). The hash workload at 0.5 Hz × ~200 processes × ~20 ns per SipHash = ~2 µs of CPU per tick, which is negligibly fast — the constraint is comfortably met regardless of the resolved tick rate.

4. **Salt rotation semantics are not specified in any locked spec.** R7 introduces `ventd ctl rotate-salt` and `ventd ctl rotate-salt --reseed-buckets` but these are not currently documented in spec-ctl. Recommend cross-coordinating with whoever owns spec-ctl during ingestion.

5. **Maintenance-class label override is a new design element.** R5 currently treats its blocklist purely as a *negative filter* (these processes do not block idle-gating). R7 reuses the list as a *positive label dictionary* (these processes get reserved signature labels). This is a coherent extension but it does mean R5's blocklist is now load-bearing for two distinct purposes; if R5 ever wants to expand its list for idle-gate reasons, the team must verify the additions are also acceptable as reserved labels (essentially they always will be, but flag for awareness).

6. **`hash/maphash` stdlib tempting but not usable.** The Go stdlib's `hash/maphash` package would be the obvious zero-dependency choice and *uses SipHash-family primitives internally*, but its Seed is per-process and explicitly cannot be persisted across daemon restarts. The R7 design therefore must use an external SipHash implementation (`dchest/siphash`). This is a one-line `go.mod` addition and not a meaningful complexity cost, but it does mean ventd carries a small external dependency for what could be a stdlib feature in a future Go release. If `hash/maphash` ever exposes a deterministic-seed mode (an open feature request in the Go issue tracker), R7 should migrate to stdlib.

---

# PART 3 — Implementation File Targets

Extending the C.5 list from the bundle:

```
internal/signature/
├── hash.go            # SipHash-2-4 wrapper, salt loading, salt rotation
├── library.go         # Library type, EWMA multiset, K-stable promotion, top-K extraction
├── persistence.go     # spec-16 KV adapter (Save/Load), msgpack schema, warm-restart
├── blocklist.go       # Re-export of R5's blocklist with reserved-label dictionary
├── reveal.go          # ventd doctor --reveal-signatures handler (paused-only)
├── config.go          # Config struct + ventd.toml binding under [smart.signature]
├── eviction.go        # Weighted-LRU eviction with τ = 14 days
└── library_test.go    # Table tests for the four flap scenarios + warm restart
```

Specifically:

- **`internal/signature/hash.go`** owns: `Hasher` type, `NewHasher(salt)`, `HashComm(comm string) uint64`, `LoadOrCreateSalt(path string) ([]byte, error)`, `RotateSalt(path string, reseedBuckets bool) error`. Imports `github.com/dchest/siphash`. Exports nothing else. ~150 LOC.

- **`internal/signature/library.go`** owns: `Library` type, `NewLibrary(cfg Config, hasher *Hasher, blocklist *Blocklist) *Library`, `Tick(now, processes) (label, promoted)`, `LookupBucket(label) *Bucket`, `UpsertBucket(label, rls)`, internal `detectMaintDominant`, `commit`, `evict`. ~400 LOC.

- **`internal/signature/persistence.go`** owns: `Save(kv store.KV) error`, `Load(kv store.KV) error`, msgpack schema constants, version-migration scaffolding for future schema bumps. ~150 LOC. Depends on the project's existing `vmihailenco/msgpack` dep already pulled in by spec-16.

- **`internal/signature/blocklist.go`** owns: `type Blocklist struct{ ... }`, `NewBlocklist() *Blocklist` (constructed from R5's source list), `IsMaintenance(comm string) (canonical string, ok bool)`. Crucially: the underlying string list is *defined in `internal/idle/blocklist.go`* (R5's home) and re-exported here. R7 does *not* duplicate the list. ~80 LOC.

- **`internal/signature/reveal.go`** owns: the `--reveal-signatures` doctor subcommand handler. Checks `ctl.IsPaused()`, renders the live mapping by walking `/proc` and SipHashing each comm, prints to stdout. Never persists. ~120 LOC.

- **`internal/signature/config.go`** owns: `Config` struct, defaults, TOML binding under `[smart.signature]`. ~80 LOC.

- **`internal/signature/eviction.go`** owns: `evictWeightedLRU(buckets, target int)` helper, score function, deterministic tie-breaking. ~60 LOC.

- **`internal/signature/library_test.go`** owns: table tests for each of the four flap scenarios (with synthetic process samples scripted to the per-tick traces shown in §Q4), warm-restart round-trip test (Save → freshly-constructed Library → Load → Tick produces same label as continuous run), salt-rotation invalidation test, blocklist override test. ~600 LOC.

Total new code: roughly 1,640 LOC including tests, of which ~1,040 LOC is implementation. This fits the "solo developer budget" constraint comfortably (about 3–4 days of focused work for a Go developer familiar with the existing ventd internal abstractions).

External dependencies added:

- `github.com/dchest/siphash` (single new dep; public-domain; ~200 LOC; no transitive deps; no CGO).

No other R7-specific dependencies. The msgpack dependency, `crypto/rand`, the spec-16 KV interface, the R5 blocklist, and the `/proc` walker are all already in the project.