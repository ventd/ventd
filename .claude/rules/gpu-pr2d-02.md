# RULE-GPU-PR2D-02: NVML wrapper in internal/hal/gpu/nvml/ uses purego only — no CGO.

The `internal/hal/gpu/nvml/` package and all files it imports directly MUST NOT contain
`import "C"`. CGO is incompatible with `CGO_ENABLED=0`, the project-wide invariant. NVML
access is provided via `internal/nvidia` which loads `libnvidia-ml.so.1` at runtime using
purego Dlopen/Dlsym. The static-analysis subtest greps the `internal/hal/gpu/nvml/`
directory tree for the literal string `import "C"` and asserts zero matches. A cgo import
that slipped in during a refactor would break the static binary on musl-based distros and
Alpine Linux, where libc SONAME assumptions in fakecgo fail at process start.

Bound: internal/hal/gpu/nvml/loader_test.go:TestNVML_NoCGO
