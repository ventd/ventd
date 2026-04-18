You are Claude Code, working on the ventd repository.

## Task
ID: ULTRA1-W-267
Track: HAL
Goal: Add unit tests for `internal/hal` registry — currently at 0.0% coverage. Resolves ultrareview-1 ULTRA-06 finding 1 (issue #267). Registry is the multi-backend composition layer that Phase 2 Wave 1 will populate; landing tests before Wave 1 dispatches keeps the composition layer defended by CI.

## Care level
LOW. Test-only change. No production code touched.

## Context

- `internal/hal/registry.go` — package under test. Exports `Register`, `Backend`, `Reset`, `Enumerate`, `Resolve`.
- `internal/hal/backend.go` — interface definitions. No coverable statements (interfaces + type declarations).
- `internal/hal/contract_test.go` — exists but lives in `hal` package for table-driven cross-backend tests; does NOT cover the registry primitives directly (it exercises them indirectly via each backend's mkCh).

## What to do

1. Create `internal/hal/registry_test.go`:

   ```go
   package hal

   import (
       "context"
       "strings"
       "sync"
       "testing"
   )

   type fakeBackend struct {
       name     string
       channels []Channel
   }

   func (b *fakeBackend) Name() string                               { return b.name }
   func (b *fakeBackend) Enumerate(context.Context) ([]Channel, error) { return b.channels, nil }
   func (b *fakeBackend) Read(Channel) (Reading, error)              { return Reading{}, nil }
   func (b *fakeBackend) Write(Channel, uint8) error                 { return nil }
   func (b *fakeBackend) Restore(Channel) error                       { return nil }
   func (b *fakeBackend) Close() error                                { return nil }

   func TestRegistry_RegisterAndBackend(t *testing.T) {
       Reset()
       b := &fakeBackend{name: "fakeA"}
       Register(b)
       got, ok := Backend("fakeA")
       if !ok || got != b {
           t.Fatalf("Backend lookup returned (%v, %v), want (b, true)", got, ok)
       }
   }

   func TestRegistry_Reset_ClearsRegistry(t *testing.T) {
       Reset()
       Register(&fakeBackend{name: "x"})
       Reset()
       if _, ok := Backend("x"); ok {
           t.Fatal("Reset did not clear registry")
       }
   }

   func TestRegistry_Enumerate_AggregatesAllBackends(t *testing.T) {
       Reset()
       Register(&fakeBackend{name: "one", channels: []Channel{{ID: "c1"}, {ID: "c2"}}})
       Register(&fakeBackend{name: "two", channels: []Channel{{ID: "c3"}}})
       got, err := Enumerate(context.Background())
       if err != nil {
           t.Fatalf("Enumerate err: %v", err)
       }
       if len(got) != 3 {
           t.Errorf("Enumerate len = %d, want 3", len(got))
       }
   }

   func TestRegistry_Resolve_Success(t *testing.T) {
       Reset()
       ch := Channel{ID: "/sys/pwm1"}
       Register(&fakeBackend{name: "fb", channels: []Channel{ch}})
       b, got, err := Resolve("fb:/sys/pwm1")
       if err != nil {
           t.Fatalf("Resolve err: %v", err)
       }
       if b == nil || b.Name() != "fb" {
           t.Errorf("Resolve backend = %v, want fb", b)
       }
       if got.ID != "/sys/pwm1" {
           t.Errorf("Resolve channel ID = %q, want %q", got.ID, "/sys/pwm1")
       }
   }

   func TestRegistry_Resolve_UnknownBackend(t *testing.T) {
       Reset()
       _, _, err := Resolve("missing:/foo")
       if err == nil {
           t.Fatal("Resolve on unknown backend returned nil error")
       }
       if !strings.Contains(err.Error(), "missing") {
           t.Errorf("error message should name the missing backend, got: %v", err)
       }
   }

   func TestRegistry_Resolve_MalformedKey(t *testing.T) {
       Reset()
       Register(&fakeBackend{name: "fb"})
       _, _, err := Resolve("no-separator-here")
       if err == nil {
           t.Fatal("Resolve on malformed key returned nil error")
       }
   }

   func TestRegistry_ConcurrentRegistration_Race(t *testing.T) {
       Reset()
       const N = 50
       var wg sync.WaitGroup
       wg.Add(N)
       for i := 0; i < N; i++ {
           i := i
           go func() {
               defer wg.Done()
               Register(&fakeBackend{name: "fb-" + string(rune('a'+i%26))})
           }()
       }
       wg.Wait()
       // No assertions on count (dedup semantics up to implementation); the
       // point is that the -race detector must find no data races on the
       // registry mutex.
   }
   ```

   Note: adjust the test bodies to match the ACTUAL signatures of `Register`, `Backend`, `Resolve`, `Reset`, `Enumerate` in `internal/hal/registry.go`. The signatures above are a working assumption — read the real file first and adapt.

2. If the actual `Resolve` signature returns only `(FanBackend, Channel, error)` with key-parsing semantics different from `<name>:<path>`, adapt the tests to the real contract. The tests must verify whatever contract registry.go actually implements; they should NOT assert invariants that don't match the code.

3. Close #267 in the PR description with `Closes #267`.

4. Build/vet/lint/test clean under `-race`:
   ```
   go test -race -count=1 -v -run TestRegistry ./internal/hal/...
   ```
   Coverage: `go test -cover ./internal/hal/` should report > 80% on registry.go (backend.go is interface-only).

## Definition of done

- `internal/hal/registry_test.go` exists with at least 6 test functions.
- `TestRegistry_ConcurrentRegistration_Race` passes under `-race`.
- Package coverage on `internal/hal` is >80% of statements.
- `Closes #267` in the PR body.
- CHANGELOG entry under `## Unreleased` / `### Added` — one line.

## Out of scope

- Changes to `registry.go` or `backend.go`. If you find a bug in the registry while testing it, file a separate issue; don't touch production code in this PR.
- Tests for individual backends (hwmon/nvml) — those live in their own packages.

## Branch and PR

- Branch: `claude/T-HAL-REGISTRY-tests`
- Title: `test(hal): add registry unit tests (closes #267, ultrareview-1)`

## Constraints

- Files: `internal/hal/registry_test.go` (new), `CHANGELOG.md`.
- No new dependencies.
- `CGO_ENABLED=0` compatible.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- Additional field: COVERAGE_BEFORE / COVERAGE_AFTER on `internal/hal` package.
