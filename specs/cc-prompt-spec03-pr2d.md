# cc-prompt-spec03-pr2d.md — GPU vendor catalog implementation

**Target:** spec-03 PR 2d (GPU vendor catalog).
**Predecessor:** spec-03 PR 2c (diag bundle, merged via #639 + ef42159).
**Source:** /mnt/project/2026-04-gpu-vendor-catalog.md §9 (implementation plan).
**Successor consumer:** spec-05-prep PR 1 (trace harness imports `internal/hal/gpu/*` read paths).

Sonnet only. No subagents. Single PR. Conventional commits.

**Branch:** `spec-03/pr-2d-gpu-catalog`
**Estimate:** $25-40, 40-60 minutes.

---

## Read first

1. `/mnt/project/2026-04-gpu-vendor-catalog.md` — full spec, especially §2 NVIDIA, §3 AMD, §4 Intel, §9 implementation plan.
2. `internal/diag/detection/gpu.go` (in repo, from PR 2c) — existing GPU detection scaffold. PR 2d extends, does not duplicate.
3. `internal/hwdb/profile_v1.go` (in repo, from PR 2a) — schema v1.0 frozen. PR 2d adds NO new schema fields, only new YAML entries.
4. `internal/hwdb/catalog/drivers/*.yaml` (in repo, from PR 2a) — existing chip-driver YAMLs. PR 2d adds GPU driver entries alongside.

---

## Scope

GPU fan control via three vendor backends, all read-paths first, write-paths capability-gated.

**NVIDIA:** purego dlopen `libnvidia-ml.so.1`, ~17-20 symbols, hand-written wrapper (NVIDIA/go-nvml is CGO, incompatible with `CGO_ENABLED=0`).
**AMD:** sysfs-direct under `/sys/class/drm/card*/device/`. Two variants: RDNA1/2 classic `pwm1`/`pwm1_enable`, RDNA3+ `gpu_od/fan_ctrl/fan_curve`.
**Intel:** sysfs read-only (kernel 6.12+ exposes `fan*_input` via xe-hwmon, no `pwm*`).

Default behavior: read-only enumeration + fan RPM reporting. Writes gated behind `--enable-gpu-write` daemon flag (matches v0.4.0 Corsair gating pattern).

---

## Files to create

### NVIDIA backend
- `internal/hal/gpu/nvml/loader.go` — purego Dlopen, RegisterLibFunc per symbol, graceful fallback if `libnvidia-ml.so.1` absent.
- `internal/hal/gpu/nvml/types.go` — `Return`, `Device` (uintptr), `FanControlPolicy`, error code constants per NVML headers.
- `internal/hal/gpu/nvml/wrappers.go` — Go-friendly methods on `*Lib`: `Init`, `Shutdown`, `DeviceCount`, `Device(idx)`, `FanSpeed(dev, fanIdx)`, `SetFanSpeed(dev, fanIdx, pct)`, `SetFanControlPolicy(dev, fanIdx, policy)`, `NumFans(dev)`.
- `internal/hal/gpu/nvml/probe.go` — capability probe per §2.4 of catalog: returns `rw_full | rw_quirk | ro_sensor_only` based on `nvmlDeviceSetFanControlPolicy` result codes.
- `internal/hal/gpu/nvml/quirks.go` — Q1-Q9 from catalog §2.5: laptop-dGPU detection (DMI chassis_type → `requires_userspace_ec`), driver-version check (R515+ for write, R520+ for policy), shutdown restore via `SetFanControlPolicy(TEMPERATURE_CONTINOUS_SW)`.
- `internal/hal/gpu/nvml/loader_test.go` — fixture-based tests, no real libnvidia-ml needed (purego.Dlopen failure path tested explicitly).
- `internal/hal/gpu/nvml/probe_test.go` — capability probe matrix (R470/R515/R520, NOT_SUPPORTED, NO_PERMISSION, FUNCTION_NOT_FOUND).

### AMD backend
- `internal/hal/gpu/amdgpu/sysfs.go` — discovery via `/sys/class/drm/card*/device/uevent` (PCI_SLOT_NAME), hwmon resolution via `name` matching (never hardcode `hwmonN`), `pwm1`/`pwm1_enable` paths for RDNA1/2.
- `internal/hal/gpu/amdgpu/fan_curve.go` — RDNA3+ 5-anchor-point curve writer, `gpu_od/fan_ctrl/fan_curve` format (`<idx> <temp> <pct>` + `c` commit + `r` reset).
- `internal/hal/gpu/amdgpu/quirks.go` — Q1-Q9 from catalog §3.6: stuck-auto-mode dance, ppfeaturemask gating, RDNA4 6.14 broken-curve detection, zero-RPM observation, multi-monitor pp_dpm warning, hwmon-N reordering, APU Vega exclusion, CoolerControl coexistence sentinel.
- `internal/hal/gpu/amdgpu/sysfs_test.go` — fixture trees under `testdata/sys/class/drm/`, RDNA1/2/3/4 variants.
- `internal/hal/gpu/amdgpu/fan_curve_test.go` — synthetic fan_curve writes, format validation.

### Intel backend
- `internal/hal/gpu/xe/sysfs.go` — RO read of `fan*_input` from xe-hwmon and i915 hwmon, kernel-version check (6.12+ required for fan reporting), explicit refusal to expose any write path.
- `internal/hal/gpu/xe/sysfs_test.go` — fixtures for DG2 i915, BMG xe.

### Catalog YAMLs (schema v1.0, no new fields)
- `internal/hwdb/catalog/drivers/nvidia.yaml` — virtual entry per catalog §2.3. No hwmon `name` exposed; matched via NVML presence + PCI vendor 0x10de.
- `internal/hwdb/catalog/drivers/amdgpu.yaml` — two variants (`rdna1_rdna2`, `rdna3_rdna4`) per catalog §3.5. Variant selection via PCI device-id + asic-revision lookup at runtime.
- `internal/hwdb/catalog/drivers/i915.yaml` — RO entry per catalog §4.2.
- `internal/hwdb/catalog/drivers/xe.yaml` — RO entry per catalog §4.2 (Battlemage).
- `internal/hwdb/catalog/drivers/nouveau.yaml` — RO sensor only, virtual entry similar to nvidia but no NVML.
- `internal/hwdb/catalog/drivers/radeon.yaml` — legacy pre-GCN, RO sensor only.

### Detection extension (PR 2c integration)
- `internal/diag/detection/gpu.go` — **extend, do not duplicate**. PR 2c shipped basic enumeration. PR 2d adds: NVML driver version, NVML library version, per-device UUID + arch, AMDGPU `ppfeaturemask` value, OverDrive bit state, per-card `gpu_metrics` snapshot, xe vs i915 driver per card, kernel version, tainted state, list of processes holding `/dev/nvidia*` and `/dev/dri/card*`.
- Update `internal/diag/detection/gpu_test.go` accordingly.

### Invariant bindings
- `.claude/rules/gpu-pr2d-01.md` through `gpu-pr2d-08.md` — eight new RULE-GPU-PR2D-* rules. See "Invariants" below. Each starts with `<!-- rulelint:allow-orphan -->` marker; strip per rule as subtests land.

---

## Files to modify

- `internal/hwdb/matcher_v1.go` — extend tier-3 fallback to include GPU driver allowlist (`amdgpu`, `nouveau`, `nvidia`, `xe`, `i915`, `radeon`). Reuse existing matcher logic, no new matcher code path.
- `cmd/ventd/main.go` — register `--enable-gpu-write` flag (default false, matches `--enable-corsair-write` pattern).
- `internal/hal/gpu/registry.go` (new file, but wires to existing HAL pattern) — backend selector: at startup, enumerate GPUs, route each to nvml/amdgpu/xe backend by driver name. Reads `name` from hwmon or detects via PCI vendor for NVIDIA proprietary.
- `CHANGELOG.md` — under v0.5.0 `### Added`: "GPU fan control via NVIDIA NVML (purego), AMDGPU sysfs (RDNA1/2 + RDNA3+ fan_curve), Intel xe/i915 read-only sensors. Behind `--enable-gpu-write` flag."

---

## Files NOT touched

- `internal/hwdb/profile_v1.go` — schema v1.0 frozen. RULE-HWDB-PR2-01 binds. No fields added.
- `internal/calibration/*` — calibration probe is fan-controller-side, not GPU-specific. spec-04 PI autotune handles GPU thermal time constants, not this PR.
- `internal/control/*` — curve application logic is HAL-agnostic. Routes through new `gpu/registry.go` like any other HAL.
- `internal/hal/corsair/*` — unrelated, no shared code.
- BIOS/firmware-modify paths anywhere — read-only by default, write-gated behind explicit flag, no firmware writes ever.

---

## Invariants — RULE-GPU-PR2D-01..08

1. **RULE-GPU-PR2D-01** — All GPU writes are gated. Default daemon mode is read-only enumeration + RPM reporting. Writes require both `--enable-gpu-write` flag AND per-device capability probe success. **Binds to:** `TestGPU_WriteGated`.

2. **RULE-GPU-PR2D-02** — NVML wrapper uses purego only. No CGO. Static-analysis subtest greps `internal/hal/gpu/nvml/` for `import "C"` — must return zero matches. **Binds to:** `TestNVML_NoCGO`.

3. **RULE-GPU-PR2D-03** — `libnvidia-ml.so.1` absence is graceful. NVML backend returns "no NVIDIA GPUs detected" without panic, daemon continues with other backends. **Binds to:** `TestNVML_GracefulMissingLib`.

4. **RULE-GPU-PR2D-04** — Schema v1.0 unchanged. New driver YAMLs validate against existing `internal/hwdb/profile_v1.go` schema. No new fields. **Binds to:** `TestHWDB_GPUEntriesV1Compatible`.

5. **RULE-GPU-PR2D-05** — hwmon path resolution by `name` only. No `hwmon0`/`hwmon1`/etc. literals in non-test code under `internal/hal/gpu/`. Static-analysis grep. **Binds to:** `TestGPU_NoHwmonNumbersHardcoded`.

6. **RULE-GPU-PR2D-06** — Laptop dGPU detection is conservative. DMI `chassis_type ∈ {laptop, notebook, sub_notebook, hand_held, convertible}` AND second GPU present AND iGPU exists → mark dGPU `requires_userspace_ec`. NVML write attempts on such devices must return early with a structured error pointing to spec-09 NBFC backend. **Binds to:** `TestNVML_LaptopDgpuRequiresEC`.

7. **RULE-GPU-PR2D-07** — RDNA3+ writes use `fan_curve` interface only. Direct `pwm1` writes are refused on RDNA3+ (matches kernel firmware refusal). Static-analysis subtest verifies the AMD backend dispatches by variant. **Binds to:** `TestAMD_RDNA3UsesFanCurve`.

8. **RULE-GPU-PR2D-08** — Intel xe/i915 backend has no write code paths. Static-analysis grep of `internal/hal/gpu/xe/` for `os.OpenFile` with write flags must return zero matches. **Binds to:** `TestXE_ReadOnly`.

---

## Success conditions

1. `go test ./internal/hal/gpu/... ./internal/hwdb/... ./internal/diag/...` passes; all 8 RULE-GPU-PR2D-* subtests bound and green.
2. `tools/rulelint` returns 0 (no orphan markers in PR 2d's rules after lands).
3. Existing PR 2a/2b/2c tests still pass — no regressions in matcher, calibration, diag bundle.
4. `golangci-lint run ./...` returns 0.
5. `go build -tags netgo -ldflags '-s -w' ./cmd/ventd` succeeds with `CGO_ENABLED=0`. Binary still works without `libnvidia-ml.so.1` present at runtime (graceful skip).
6. `go list -deps ./cmd/ventd | grep internal/hal/gpu` confirms package wired in (PR 2c's ghost-code regression check applies — verify with `go list -deps ./cmd/ventd | grep internal/hal/gpu/nvml` and similarly for amdgpu, xe).
7. Synthetic-fixture e2e test: dev container with no GPUs, daemon enumerates zero GPUs without panic, JSON output of detection shows empty GPU array. (Real-hardware validation is post-merge on Phoenix's RTX 4090 desktop.)
8. `--enable-gpu-write` flag documented in `--help` output and in `docs/` if a relevant doc page exists; otherwise skip docs (out of scope to write GPU-control user guide in this PR).

---

## Verification before marking done

```
1. go test ./internal/hal/gpu/... -v -count=1
2. go test ./... -count=1                   # no regressions
3. golangci-lint run ./...
4. tools/rulelint                           # zero allow-orphan in PR 2d's new rules
5. CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd
6. go list -deps ./cmd/ventd | grep -E 'internal/hal/gpu/(nvml|amdgpu|xe)'
   # all three must appear — no ghost code
7. ./bin/ventd --help | grep enable-gpu-write
8. ./bin/ventd                              # default run, dev container, zero GPUs
   # daemon starts, enumerates zero GPUs gracefully, no panic
```

---

## Stop and surface to Phoenix if

- NVML purego wrapper grows beyond ~25 symbols — surface, scope-creep risk. Catalog §2.2 caps at 17-20.
- A RULE-GPU-PR2D-* invariant cannot be bound 1:1 to a subtest — surface, redesign the invariant before continuing.
- `internal/hal/gpu/registry.go` accumulates non-trivial logic (>100 LOC of dispatch) — surface, may indicate the HAL pattern needs a small refactor outside this PR.
- AMD RDNA3 fan_curve write fails capability probe on synthetic fixture but spec says it should work — surface, may indicate fixture is wrong or catalog §3.3 needs a clarification.
- Detection extension to `internal/diag/detection/gpu.go` triggers more than 5 new redactor primitives in PR 2c's redactor — surface, redaction scope is frozen at PR 2c's 10 primitives unless threat model amendment is filed.
- Total CC spend crosses $35 — surface progress, request continuation.

---

## Why this is bounded at $25-40

Substantial code reuse from PR 2c:
- `internal/diag/detection/gpu.go` already exists, PR 2d extends it rather than rewriting.
- Schema v1.0 is frozen, no new field design work.
- `internal/hwdb/matcher_v1.go` allowlist extension is one-line per driver.

NVML purego wrapper is the largest novel-code chunk. Bounded by:
- Catalog §2.4 has the loader skeleton already written.
- Catalog §2.2 lists exact symbols.
- Capability probe is one function returning an enum.
- Quirks are nine bounded one-shot cases, not algorithms.

AMD backend reuses existing hwmon path-resolution logic (RULE-GPU-PR2D-05 enforces the same `name`-matching pattern hwdb already uses).

Intel backend is trivial — file reads only.

Risks that would inflate cost:
- Multi-GPU UUID stability code path on hot-plug — defer to v0.6.0 if it surfaces.
- Hybrid laptop AC-vs-battery detection — RULE-GPU-PR2D-06 covers the static case (laptop → requires_userspace_ec). Dynamic AC/battery switching is spec-04 cross-cut, not this PR.

---

## PR description must call out

- "spec-03 PR 2d adds GPU vendor catalog. Schema v1.0 unchanged. Writes gated behind `--enable-gpu-write` flag."
- "NVML wrapper is hand-written purego thin wrapper, not a fork of NVIDIA/go-nvml (which is CGO and incompatible)."
- "Real-hardware validation pending on Phoenix's RTX 4090 + AMD acquisition. Synthetic fixtures cover all logic paths."
- "spec-05-prep PR 1 imports `internal/hal/gpu/*` read paths for trace harness; API stability promised within v1.x."

---

**End of cc-prompt-spec03-pr2d.md.**
