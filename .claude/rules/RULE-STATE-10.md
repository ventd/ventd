# RULE-STATE-10: The state directory `/var/lib/ventd/` MUST exist after first daemon start; absence triggers initialisation, not failure.

`state.Open(dir, logger)` calls `initDirs(dir)` which uses `os.MkdirAll` to create:

- `dir/` (mode 0755)
- `dir/models/` (mode 0755)
- `dir/logs/` (mode 0755)

If `dir` does not exist, `initDirs` creates the full hierarchy. If it already
exists, `os.MkdirAll` is a no-op. A missing state directory is therefore not
an error — it is the normal first-boot condition. All three stores (KV, Blob,
Log) are initialised with empty state on a fresh directory. This allows the
daemon to start cleanly after `rm -rf /var/lib/ventd/` without requiring
manual directory creation or a special `--reset` flag.

Bound: internal/state/state_test.go:TestRULE_STATE_10_DirectoryBootstrap
