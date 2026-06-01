# Config hwmon path resolution rules

Rules governing how `internal/config` re-anchors hwmon sensor / fan paths to
the live hwmon class directory at load time.

## RULE-CONFIG-HWMON-ROOT-OVERRIDE: Hwmon path resolution honours VENTD_HWMON_ROOT — under the override the resolver scans the synthetic tree, never the host's real /sys.

`config.Load` re-anchors each hwmon sensor / fan whose `ChipName`
is set by matching the chip against the hwmon class directory and
rewriting the path's `/hwmonN/` segment to the current index
(`ResolveHwmonPaths`) — so a config survives a hwmonN renumber
across reboots. The directory it scans is resolved by
`resolveHwmonRootFS()`:

1. an explicit `SetHwmonRootFS` value (tests) always wins;
2. else when `VENTD_HWMON_ROOT` (`hwmon.RootIsOverridden`)
   redirects hwmon to a synthetic tree (`tools/hwmonsim`), the
   resolver scans `hwmon.EffectiveRoot()`;
3. else `/sys/class/hwmon` — the production default, unchanged.

Without honouring the override at step 2, the resolver scans the
host's real `/sys`, finds the config's chip at the host's index
(e.g. an `nct6687` at `hwmon9`), and rewrites the sim path's
`/hwmon0/` segment to `/hwmon9/` — re-anchoring a sim config onto
a non-existent `hwmonN` *under the override root*
(`<root>/hwmon9/...`). The controller then reads/writes a path
that does not exist, so a config-driven daemon cannot drive the
simulated fans at all. This is the config-load companion to
`RULE-PROBE-12` (probe enumeration) and the `validateHwmonSysfsPath`
override allow-list: all three teach the daemon's hwmon paths to
follow `VENTD_HWMON_ROOT` together, so the whole stack runs against
`tools/hwmonsim` and never the real hardware.

Bound: internal/config/config_test.go:RULE-CONFIG-HWMON-ROOT-OVERRIDE_resolves_against_sim_tree
