# fix-298-cache-hardening

You are Claude Code. Harden two controller cache-invalidation edges per issue #298.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-298-cache-hardening origin/main
test ! -f .cowork/prompts/fix-298-cache-hardening.md && echo "OK: working tree is main" || {
    echo "ERROR: cowork/state files present. Abort."
    exit 1
}
```

If the sanity check fails, stop and report.

## Context (#298 summary)

PR #260 (hot-loop alloc optimisations) introduced two caches that have subtle invalidation gaps:

1. **`curveSig` misses slice fields.** Fingerprint captures scalars only. Production is safe (config swaps are pointer-new), but in-place slice mutations (`live.Curves[0].Points[i].Temp = X`) hit the cache with stale anchors. Foot-gun for test authors.

2. **`maxRPM` cache locks in the 2000 fallback.** On a transient `fan*_max` read failure during first-tick sysfs enumeration (udev race, bind/unbind), `ReadFanMaxRPM` returns its 2000 fallback; `maxRPMCached = true` locks it in for the daemon's lifetime. A GPU whose real `fan*_max` is 5000+ gets capped at 40% capacity silently.

Cassidy recommends option (a) for both: extend fingerprint, and re-read on the fallback sentinel.

## Required changes

### 1. `internal/controller/controller.go` — extend `curveSig`

Add import: `"encoding/binary"`, `"hash/fnv"`.

Extend the struct:

```go
type curveSig struct {
    typ, sensor, function        string
    minTemp, maxTemp, hysteresis float64
    smoothing                    time.Duration
    minPWM, maxPWM, value        uint8
    pointsHash, sourcesHash      uint64 // FNV-64a of Points/Sources contents
}
```

Update the `curveSigOf` constructor (or equivalent — read the file to find the exact factory name):

```go
func curveSigOf(c config.CurveConfig) curveSig {
    sig := curveSig{
        typ:        c.Type,
        sensor:     c.Sensor,
        function:   c.Function,
        minTemp:    c.MinTemp,
        maxTemp:    c.MaxTemp,
        hysteresis: c.Hysteresis,
        smoothing:  c.Smoothing,
        minPWM:     c.MinPWM,
        maxPWM:     c.MaxPWM,
        value:      c.Value,
    }
    h := fnv.New64a()
    for _, p := range c.Points {
        _ = binary.Write(h, binary.LittleEndian, p.Temp)
        h.Write([]byte{p.PWM})
    }
    sig.pointsHash = h.Sum64()
    h.Reset()
    for _, s := range c.Sources {
        h.Write([]byte(s))
        h.Write([]byte{0}) // separator to avoid ['ab','c'] == ['a','bc']
    }
    sig.sourcesHash = h.Sum64()
    return sig
}
```

Use the exact field names from `config.CurveConfig` — verify by reading `internal/config/config.go` first. The types here (`Points []CurvePoint`, `Sources []string`) are from the issue body; confirm against current code.

Struct equality via `==` still works (both hash fields are `uint64`). No other change needed to the cache check.

### 2. `internal/controller/controller.go` — maxRPM fallback re-read

Replace:

```go
if !c.maxRPMCached {
    c.maxRPM = hwmon.ReadFanMaxRPM(c.pwmPath)
    c.maxRPMCached = true
}
```

With:

```go
// Treat 2000 as "fallback — retry next tick" per #298. A real amdgpu
// fan*_max that happens to equal 2000 gets re-read every tick, which is
// cheap (one os.ReadFile) and harmless. Real GPU fans are typically in
// 3000-5000 range so false-positive rate is near zero.
if c.maxRPM <= 0 || c.maxRPM == 2000 {
    c.maxRPM = hwmon.ReadFanMaxRPM(c.pwmPath)
}
```

Delete `maxRPMCached` field from the `channelState` struct (or equivalent owning struct — whatever currently holds it). The presence-as-bool is replaced by value-as-sentinel.

### 3. Regression tests

Add two tests to `internal/controller/controller_test.go`:

```go
// regresses #298
func TestCurveSig_CoversPointsAndSources(t *testing.T) {
    base := config.CurveConfig{
        Type: "points",
        Points: []config.CurvePoint{{Temp: 40, PWM: 30}, {Temp: 70, PWM: 80}},
    }
    sig1 := curveSigOf(base)
    mutated := base
    mutated.Points = []config.CurvePoint{{Temp: 40, PWM: 30}, {Temp: 75, PWM: 80}}
    sig2 := curveSigOf(mutated)
    if sig1 == sig2 {
        t.Fatal("curveSig did not change when Points mutated")
    }
    // Symmetric check for Sources if the current curve model uses it for mix curves.
}

// regresses #298
func TestMaxRPM_FallbackTriggersReread(t *testing.T) {
    // Simulate first read returns 2000 (fallback), second read returns 4500 (real).
    // Assert: controller re-reads after first tick, caches 4500 from tick 2 onwards.
}
```

For `TestMaxRPM_FallbackTriggersReread`, use whatever injection pattern the codebase already has for `hwmon.ReadFanMaxRPM` — if there's no seam, mention it in CONCERNS and skip that specific test (file a follow-up for the seam).

## Allowlist

- `internal/controller/controller.go`
- `internal/controller/controller_test.go`
- `CHANGELOG.md`

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/controller/...
gofmt -l internal/controller/
go vet ./internal/controller/...
```

Also benchmark to confirm the hashing cost is negligible:

```bash
go test -bench=. -benchmem ./internal/controller/ | grep -i 'tick\|curveSig'
```

Report the per-op numbers in the PR body. Expected: ~100ns per tick overhead for the hashing, zero alloc delta.

## PR

Open READY (not draft). Title: `fix(controller): curveSig covers Points/Sources; maxRPM retries on 2000 fallback (closes #298)`

PR body: Fixes #298, BRANCH_CLEANLINESS block, CHANGELOG entry under `### Fixed`:

> `controller: curveSig fingerprint now covers Points and Sources slice contents, eliminating stale-anchor cache hits on in-place mutations; maxRPM cache re-reads when the sentinel 2000 RPM fallback was captured from a transient sysfs failure (closes #298)`

Include BENCH output in PR body.

## Constraints

- Atlas merges. Do NOT merge.
- Do NOT alter the broader hot-loop alloc optimisations from #260 — this is additive hardening.
- Do NOT change the 2000 fallback constant itself — only the cache semantics around it.
- Single commit.
- If the `channelState`/`c.maxRPMCached` field name differs from this prompt, match actual code.

## Reporting

- STATUS: done | blocked
- PR URL
- `go test -race -count=1 ./internal/controller/...` tail
- Bench output (hot-loop tick cost before/after)
- Lines changed
- CONCERNS if `hwmon.ReadFanMaxRPM` lacks a test seam and the fallback-re-read test couldn't be added
