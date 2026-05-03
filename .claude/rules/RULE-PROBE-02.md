# RULE-PROBE-02: Virtualisation detection requires ≥3 independent sources before setting Virtualised=true.

`detectEnvironment` scores **four** independent virt signals and sets
`RuntimeEnvironment.Virtualised` only when the score reaches 3: (1) DMI
`sys_vendor` / `product_name` substring match against `virtVendors`; (2)
`systemd-detect-virt --vm` exits 0 with output other than `"none"` or
`""`; (3) existence of `/sys/hypervisor`; (4) the cpuid `hypervisor`
flag in `/proc/cpuinfo` flag list. The 4th source was added 2026-05-03
to close the MicroVM/Firecracker recall gap — those hosts can fire only
the cpuid hypervisor bit (no DMI strings, no `/sys/hypervisor`, no
systemd-detect-virt on minimal images) and would otherwise score ≤2
and pass as bare-metal.

The threshold stays at 3 (now of 4) — widening recall without lowering
the precision bar. With ≤2 sources the field remains false. False-positive
virt detection on bare-metal hardware would cause ventd to refuse
installation on real systems; the ≥3-of-4 threshold trades recall
(missing a novel hypervisor) for precision (never refusing a valid
bare-metal host).

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-02_virt_requires_3_sources
