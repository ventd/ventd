# RULE-EXPERIMENTAL-FLAG-PRECEDENCE: CLI flags override config-file values; OR-merge satisfies CLI > config > default for additive boolean flags.

`experimental.Merge(cli, cfg Flags) Flags` computes each output field as `cli.Field || cfg.Field`.
Because `flag.Bool` cannot distinguish an explicit `--enable-*=false` from the default false,
OR-merge is the correct rule: a flag enabled by either source is active; a flag disabled by both
sources is inactive. This satisfies CLI > config > default for all four experimental flags.
The test fixture verifies: CLI-true beats config-false, config-true propagates when CLI is false,
both-true yields true, both-false yields false, and multiple flags are merged independently.

Bound: internal/experimental/parse_test.go:TestMerge_PrecedenceCLIOverConfig
