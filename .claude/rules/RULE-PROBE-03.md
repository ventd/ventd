# RULE-PROBE-03: Containerisation detection requires ≥2 independent sources before setting Containerised=true.

`detectEnvironment` scores three independent container signals and sets
`RuntimeEnvironment.Containerised` only when the score reaches 2: (1) existence of `/.dockerenv`;
(2) `/proc/1/cgroup` content containing a container runtime keyword (`docker`, `lxc`, `kubepods`,
`garden`); (3) `systemd-detect-virt --container` exits 0 with output other than `"none"` or `""`.
With ≤1 source the field remains false. A single-source false positive (e.g., a stale `.dockerenv`
on a reinstalled system) would incorrectly refuse installation. Two-source confirmation makes
accidental refusal essentially impossible on real hardware.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-03_container_requires_2_sources
