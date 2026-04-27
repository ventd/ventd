# RULE-PROBE-02: Virtualisation detection requires ≥3 independent sources before setting Virtualised=true.

`detectEnvironment` scores three independent virt signals and sets `RuntimeEnvironment.Virtualised`
only when the score reaches 3: (1) DMI `sys_vendor` / `product_name` substring match against
`virtVendors`; (2) `systemd-detect-virt --vm` exits 0 with output other than `"none"` or `""`; (3)
existence of `/sys/hypervisor`. With ≤2 sources the field remains false. False-positive virt
detection on bare-metal hardware would cause ventd to refuse installation on real systems — the
three-source threshold trades recall (missing a novel hypervisor) for precision (never refusing a
valid bare-metal host).

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-02_virt_requires_3_sources
