# Go Conventions — ventd

- Use `log/slog` for all logging, never `fmt.Println` or `log.Printf`
- Error wrapping: `fmt.Errorf("context: %w", err)` — always wrap with context
- Table-driven tests with `t.Run()` subtests
- No `init()` functions — explicit initialization in main or constructors
- `internal/` packages are not importable outside the module — keep it that way
- Prefer `context.Context` propagation for cancellation in long-running goroutines
- `go vet ./...` and `go test -race ./...` must pass before commit
