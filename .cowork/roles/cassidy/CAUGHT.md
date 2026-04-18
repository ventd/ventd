# Cassidy bug-catch registry

Append-only index of real bugs caught during review. A bug qualifies when:

- It would cause observable user-facing or data-correctness damage if shipped, AND
- Cassidy caught it by reading the diff — not by CI or a test failure

Semantic-drift concerns, doc-gaps, brittleness, and hardening suggestions are **not** listed here — they go in the filed-issues registry in the worklog state-summary. This file is specifically for the pattern library: "what kind of line on a diff made me look twice."

Each entry records:

- Issue # and PR that introduced it
- Bug class (used for pattern-matching on future reads)
- Keyword(s) in the diff that triggered suspicion
- One-line root-cause pattern
- One-line failure mode if shipped
- Status

---

## Index

| # | Bug class | Introducing PR | Issue | Status |
|---|---|---|---|---|
| 1 | TOCTOU / ordering race | #225 | #289 concern 1 | FIXED (PR #294) |
| 2 | namespace collision | #223 | #293 | OPEN |
| 3 | cache-poisoning / fallback-locked | #260 | #298 Opt-4 | OPEN |
| 4 | test-fixture drift from production | #281 | #305 | OPEN |
| 5 | silent-error (no err on failure path) | #285 | #307 (primary) | OPEN |
| 6 | wrong-default heuristic | #285 | #307 (secondary) | OPEN |
| 7 | durability gap (missing fsync) | #261 | #311 | OPEN |
| 8 | concurrency / accept-loop stall | #233 | #312 (primary) | OPEN |
| 9 | duplicate enumeration | #279 | #316 | OPEN |

---

## Details

### #1 — scheduler↔override race (TOCTOU / ordering)

- **Introducing PR:** #225 (scheduled profile switching)
- **Issue:** #289 concern 1 — **FIXED** in PR #294
- **Keyword:** `applyProfile(...)` immediately followed by `markManualOverride()` — two-step state change where the scheduler goroutine observes in between
- **Pattern:** when a handler mutates an atomic-pointer config AND a sidecar flag, set the sidecar BEFORE the atomic swap so observers see either old-state-with-flag-set or new-state-with-flag-set, never the unflagged in-between window
- **Failure if shipped:** operator picks profile X, scheduler tick within ~50ms overwrites with profile Y, source="schedule". Looks like a UI bug; is actually a state bug.

### #2 — sensor/fan name collision in sparkline keyspace (namespace collision)

- **Introducing PR:** #223 (sparklines)
- **Issue:** #293
- **Keyword:** `historyBuf[sensor.Name]` and `historyBuf[fan.Name]` on the same map, with `validate()` enforcing uniqueness inside each namespace but not across them
- **Pattern:** two independent input namespaces funnelled into a single keyspace downstream — validate() must enforce cross-namespace uniqueness or downstream must use compound keys
- **Failure if shipped:** config with a sensor named "cpu" and a fan named "cpu" passes validate, then the sparkline shows interleaved °C-and-duty-% values for the "cpu" key. PWM control unaffected (cosmetic), but misleading data.

### #3 — maxRPM cache locks in 2000 RPM fallback (cache-poisoning / fallback-locked)

- **Introducing PR:** #260 (hot-loop perf — Opt-4)
- **Issue:** #298 Opt-4
- **Keyword:** `c.maxRPMCached = true` set unconditionally after a call to a helper that silently returns a fallback value on error
- **Pattern:** "cache a lookup forever after first call" + "helper returns fallback on error without signalling it" — the cache locks in the fallback when the first lookup races with udev / hotplug
- **Failure if shipped:** GPU with real `fan*_max = 4800` suffers a transient sysfs race at daemon startup; maxRPM reads 2000 once and is cached; fan capped at 40% capacity for the daemon's lifetime.

### #4 — fakehid close semantics diverge from production go-hid (test-fixture drift)

- **Introducing PR:** #281 (usbbase USB HID primitive layer)
- **Issue:** #305
- **Keyword:** `func (h *fakeHandle) Close() error { return nil }` — a fake that makes Close a no-op when the real library's Close frees a handle and subsequent calls on it return ENODEV
- **Pattern:** fake fixtures that successfully compile against the interface but silently simulate a more-forgiving version of reality. Tests pass on the fake and fail on real hardware, or worse, pass on both but mask a real bug that only surfaces in production.
- **Failure if shipped:** any USB HID backend logic that relies on "Close + subsequent read fails" (for disconnect detection) passes tests on fakehid but deadlocks on real go-hid.

### #5 — IPMI Restore silently returns nil on non-zero completion code (silent-error)

- **Introducing PR:** #285 (native IPMI backend)
- **Issue:** #307 primary
- **Keyword:** `if cc != 0 { ... } return nil` — non-zero BMC completion code logged but the function returns nil
- **Pattern:** last-line-of-defence paths (Restore, Close, panic-recovery) that swallow errors so callers think "everything is fine" when the operation actually failed. Restore MUST surface failure because the caller decides whether to retry, alarm, or escalate.
- **Failure if shipped:** watchdog / ctx-cancel triggers Restore on an IPMI-backed fan; BMC is busy and returns cc=0x81; Restore logs and returns nil; fan stays at the daemon's last PWM write until the next boot. Most-important safety invariant in the whole codebase violated.

### #6 — Supermicro zone heuristic wrong for most boards (wrong-default)

- **Introducing PR:** #285 (native IPMI backend)
- **Issue:** #307 secondary
- **Keyword:** zone index derived from `dmi.ProductName` via a match table that covers maybe 3 SKUs, with `zone = 0` as the unexamined fallback
- **Pattern:** heuristic defaulting to a value that looks sane in isolation but silently wrong-routes writes on the common case. Heuristics where "I don't know" is a valid answer should return an error, not a default.
- **Failure if shipped:** Supermicro board not in the match table writes to zone 0; BMC silently accepts the write to a non-existent zone; no RPM change; operator thinks "ventd doesn't work on my Supermicro."

### #7 — persistModule missing fsync before atomic rename (durability gap)

- **Introducing PR:** #261 (persistModule atomic-rename)
- **Issue:** #311
- **Keyword:** `os.WriteFile(tmp, ...)` followed by `os.Rename(tmp, dst)` without `f.Sync()` / `file.Sync()` / `dir.Sync()` between them
- **Pattern:** atomic-rename idiom without fsync. The rename is atomic at the filesystem layer, but if the machine loses power before the OS flushes page cache, the dst file appears but is zero-length.
- **Failure if shipped:** `--probe-modules` writes a fresh ventd.conf.tmp, renames it over ventd.conf, power dies before flush; next boot has zero-length ventd.conf; no modules load; no fan control.

### #8 — TLS sniffer Accept-loop stall (availability)

- **Introducing PR:** #233 (http→https sniff listener)
- **Issue:** #312 primary
- **Keyword:** `io.ReadFull(conn, peek)` executed inline in the `Accept()` loop before the connection is handed to a goroutine
- **Pattern:** any per-connection work done before spawning the handler goroutine is serialised through Accept and is a DoS primitive. One slow / silent client stalls the whole listener.
- **Failure if shipped:** attacker opens a TCP connection to the TLS port, sends nothing, `io.ReadFull` blocks forever, new legitimate TLS connections can't be accepted. Single-socket DoS.

### #9 — halhwmon and halasahi both enumerate macsmc_hwmon (duplicate enumeration)

- **Introducing PR:** #279 (hal/asahi Apple Silicon backend)
- **Issue:** #316
- **Keyword:** halasahi's `Enumerate` calls `hwmonpkg.EnumerateDevices()` without filtering out channels the `hwmon` backend also enumerates
- **Pattern:** a "delegates to X" backend that doesn't register an X-path exclusion. The registry ends up with duplicate tagged channels; the controller writes the same PWM through two code paths per tick.
- **Failure if shipped:** on M-series hardware, every fan appears twice in the registry; every fan write happens twice per tick; calibrate sweeps walk the same channel under two different backend IDs; e2e tests fail once someone adds a "no duplicate channels" assertion to the contract test.

---

## Pattern library (extracted from the 9 entries)

Keywords that trigger a second look on any diff:

- `atomic.Store(...)` followed by another mutation on the next line → **TOCTOU** candidate
- Map keyed by a user-supplied name with no visible cross-namespace validation → **namespace collision** candidate
- `cached = true` after a call to a helper that can return a fallback value → **cache-poisoning** candidate
- Fake fixture with `Close() error { return nil }` or similar no-op method → **test-fixture drift** candidate
- `if err != nil { log; return nil }` in a Restore / Close / recover-from-panic path → **silent-error** candidate
- Heuristic with an implicit `= 0` or `= "default"` fallback → **wrong-default** candidate
- `os.WriteFile(tmp); os.Rename(tmp, dst)` without an intervening `Sync()` → **durability gap** candidate
- Per-connection blocking work inside an `Accept()` loop → **accept-stall** candidate
- A backend that "delegates" to another backend — look at both Enumerate paths → **duplicate enumeration** candidate

Next-session discipline: on every PR I deep-audit, grep the diff for these patterns first. Each hit is a 30-second check that catches a class of bug that's already bitten us nine times.
