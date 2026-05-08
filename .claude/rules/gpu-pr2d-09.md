# RULE-GPU-PR2D-09: nvidia.InitWithDeadline returns wrapped ErrLibraryUnavailable on timeout; the in-flight dlopen goroutine is orphaned (uncancellable).

`InitWithDeadline(ctx, logger, timeout)` wraps `Init` in a goroutine and selects between
the goroutine's done-channel, `time.After(timeout)`, and `ctx.Done()`. The four branches:

- **Loader returns within deadline**: error (or nil) is returned verbatim so callers'
  `errors.Is(err, ErrLibraryUnavailable)` and `errors.Is(err, ErrInitFailed)` checks
  continue to work as if `Init` had been called directly.
- **Timeout fires before loader returns**: an error wrapping `ErrLibraryUnavailable`
  whose message contains `"timed out"` and the deadline. A WARN line is logged once
  ("NVML init timed out; GPU features disabled for process lifetime").
- **ctx cancelled before loader returns**: an error wrapping `ErrLibraryUnavailable`
  with the cancellation cause embedded. No log line — cancellation is operator-driven.
- **timeout <= 0**: timeout is disabled (equivalent to plain `Init`). A pre-cancelled ctx
  still short-circuits before any loader call.

The in-flight goroutine is orphaned on timeout/cancel because `purego.Dlopen` is
uncancellable — Linux dlopen has no per-call timeout primitive, and goroutines cannot
be killed externally without coopt. The goroutine eventually completes when Dlopen
returns, which may be never on a truly wedged driver. Subsequent callers of `Init` or
`InitWithDeadline` within the same process will block on the same `loadOnce.Do` that
the orphan owns — so once a timeout fires, NVML is permanently disabled for this
process. By design: the daemon proceeds without GPU features rather than hang past
systemd's `TimeoutStartSec` with no diagnostic the operator can act on.

The testable inner core `initWithDeadline(ctx, logger, timeout, fn)` accepts an
arbitrary loader function so tests can exercise every branch without touching the
package-level `loadOnce` / `loadErr` / `loadLibraryFn` state. Production calls always
pass `Init` as the fn.

Bound: internal/nvidia/init_deadline_test.go:TestInitWithDeadline_Rules/RULE-GPU-PR2D-09_timeout_returns_wrapped_unavailable
Bound: internal/nvidia/init_deadline_test.go:TestInitWithDeadline_Rules/RULE-GPU-PR2D-09_fast_path_passes_through
Bound: internal/nvidia/init_deadline_test.go:TestInitWithDeadline_Rules/RULE-GPU-PR2D-09_ctx_cancel_returns_wrapped_unavailable
Bound: internal/nvidia/init_deadline_test.go:TestInitWithDeadline_Rules/RULE-GPU-PR2D-09_zero_timeout_disables_deadline
Bound: internal/nvidia/init_deadline_test.go:TestInitWithDeadline_Rules/RULE-GPU-PR2D-09_nil_logger_uses_default
