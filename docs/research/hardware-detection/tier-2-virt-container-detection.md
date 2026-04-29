# R1 — Tier-2 Detection Signal Reliability (Virtualization + Containers)

**Target spec:** `spec-v0_5_1-catalog-less-probe.md` (Tier-2 detection layer)
**Constraint reaffirmed:** Pure Go, `CGO_ENABLED=0`. Detection MUST be performed by reading pseudo-filesystems (`/proc`, `/sys`, `/run`) and DMI sysfs only. Shelling out to `systemd-detect-virt` or `virt-what` is explicitly forbidden, but their canonical signal lists are the correctness reference.

---

## 1. Executive summary

The runtime classification problem ventd faces is not "what hypervisor is below me?" but rather "is it safe to write to a hwmon PWM here?" Those are very different questions, and the Tier-2 layer must be designed around the second one. A Linux KVM guest with PCIe passthrough of an LPC/Super-I/O chip *legitimately* owns real fans; a Proxmox LXC container with DMI bleed-through *appears* to own real fans but actually shares /sys/class/hwmon with the host kernel and could fight Proxmox's own fan governance; an unprivileged Docker container has read-only sysfs and cannot write at all. The same physical signal — DMI vendor `Intel Corporation` — has three completely different safety implications across those three cases.

The defensible architecture, mirroring the design choice systemd made in `src/basic/virt.c`, is:

1. **Container detection always wins over VM detection**, because any container layer (LXC/Docker/Podman/nspawn/k8s/WSL) implies a shared kernel with an unknown owner of `/sys/class/hwmon`.
2. **Cgroup / namespace / containerenv signals are checked first**, because they are the only signals that the container manager itself sets and cannot be hidden by DMI obfuscation.
3. **DMI is checked next**, because it is forged-but-rarely (and forged-DMI is an explicit "VM is hiding from me" signal that should map to BLOCK, not ALLOW).
4. **CPUID's `hypervisor` bit and the `XenVMMXenVMM`/`KVMKVMKVM`/`Microsoft Hv` leaf-0x40000000 vendor strings** are the kernel-truth fallback (matching `arch/x86/kernel/cpu/hypervisor.c` and systemd's `detect_vm_cpuid`). However, CPUID requires either `CPUID` Go assembly (still CGO-free) or simply trusting `/proc/cpuinfo`'s `hypervisor` flag — ventd should use the latter for portability.
5. **WSL is its own special case** detected post-VM via the `microsoft`/`WSL` substring in `/proc/sys/kernel/osrelease`, because WSL2 is a Hyper-V VM but is *also* effectively a container from a hardware-access standpoint and must always BLOCK.

This document specifies the full table, the precedence chain, the Go-pseudocode skeleton, the false-positive/false-negative inventory, and the BLOCK/ALLOW/OVERRIDE policy ventd should ship in v0.5.1.

---

## 2. Canonical sources reviewed

| Source | URL | Why authoritative |
|---|---|---|
| systemd `src/basic/virt.c` (main branch) | https://github.com/systemd/systemd/blob/main/src/basic/virt.c | The de-facto canonical signal list. `detect_vm()` and `detect_container()` define the order, the DMI vendor table, the CPUID strings, and the WSL/proot/nspawn special cases that almost every other implementation copies. |
| systemd `detect_vm_cpuid` / DMI vendor table | https://github.com/systemd/systemd/blob/v239/src/basic/virt.c (older revision used here because the search index returned more readable content from v239; semantically unchanged on main) | Lists exact CPUID vendor strings (`KVMKVMKVM`, `VMwareVMware`, `Microsoft Hv`, `XenVMMXenVMM`, `bhyve bhyve `, `QNXQVMBSQG`, ` lrpepyh vr` for Parallels) and DMI sys_vendor strings (`QEMU`, `VMware`, `VMW`, `innotek GmbH`, `VirtualBox`, `Oracle Corporation`, `Xen`, `Bochs`, `Parallels`, `BHYVE`, `Hyper-V`, `Apple Virtualization`, `Google Compute Engine`, `Amazon EC2`). |
| systemd-detect-virt(1) manpage | https://man7.org/linux/man-pages/man1/systemd-detect-virt.1.html and https://www.freedesktop.org/software/systemd/man/latest/systemd-detect-virt.html | Documents the public taxonomy: `qemu`, `kvm`, `amazon`, `zvm`, `vmware`, `microsoft`, `oracle`, `powervm`, `xen`, `bochs`, `uml`, `parallels`, `bhyve`, `qnx`, `acrn`, `apple`, `sre`, `google`; container set: `openvz`, `lxc`, `lxc-libvirt`, `systemd-nspawn`, `docker`, `podman`, `rkt`, `wsl`, `proot`, `pouch`. Also documents the rule: "if both machine and container virtualization are used in conjunction, only the latter will be identified". |
| Lennart Poettering, "systemd for Administrators, Part XIX" | http://0pointer.de/blog/projects/detect-virt.html | States the design principle that detection libraries return only the "inner-most" virtualization, and that ConditionVirtualization in unit files exists precisely because conditionalizing services on virt-type is a legitimate use. |
| Linux kernel `arch/x86/kernel/cpu/hypervisor.c` | https://github.com/torvalds/linux/blob/master/arch/x86/kernel/cpu/hypervisor.c | The kernel's own hypervisor probe order: Xen-PV → Xen-HVM → VMware → Hyper-V (`ms_hyperv`) → KVM → Jailhouse → ACRN. This is the source of the `hypervisor` flag in `/proc/cpuinfo` (set when `cpu_has(c, X86_FEATURE_HYPERVISOR)`). |
| virt-what (Red Hat / Richard W.M. Jones) | https://people.redhat.com/~rjones/virt-what/ and https://github.com/rwmjones/virt-what (note: rwmjones GitHub actually hosts `rhsrvany`; `virt-what` is canonically distributed via RHJ's people.redhat.com page) | Heuristic-based shell script. Notable for documenting the explicit warning: "Most of the time, using this program is the wrong thing to do. Instead you should detect the specific features you actually want to use." This warning applies equally to ventd: ventd should ultimately probe for hwmon writability, with virt-detection as a guardrail, not the sole gate. |
| zcalusic/sysinfo `hypervisor.go` | https://github.com/zcalusic/sysinfo/blob/master/hypervisor.go | Pure-Go (uses Go-assembly CPUID via the `cpuid` subpackage, NOT CGO). Maps CPUID vendor → name and reads `/sys/hypervisor/type` for Xen-PV. Confirmed CGO-free, BSD-style license. Suitable for direct vendoring. |
| shirou/gopsutil v4 `host.Virtualization()` | https://github.com/shirou/gopsutil/blob/master/host/host_linux.go and https://pkg.go.dev/github.com/shirou/gopsutil/v4/host | Returns `(system, role, error)` where role ∈ `{"guest","host"}`. Documented as CGO-free on Linux: README states "All works are implemented without cgo by porting C structs to Go structs" and the only CGO file in the tree is `host_darwin_cgo.go` (Darwin-only and disabled when `CGO_ENABLED=0`). Suitable for use in ventd, but its detection logic is a subset of systemd's — it does not distinguish dom0 vs domU and merges KVM/QEMU. |
| Microsoft WSL issue #423 (osrelease convention) | https://github.com/microsoft/WSL/issues/6911 and https://github.com/microsoft/WSL/issues/11814 | Documents that `/proc/sys/kernel/osrelease` contains the literal substring `microsoft` (WSL1 historically `Microsoft`, WSL2 `microsoft-standard-WSL2+`). This is the systemd-canonical detection. Case-insensitive match required; smallstep/cli #332 is a reference for the case-sensitivity bug class. |
| Podman `/run/.containerenv` documentation | https://docs.podman.io/en/latest/markdown/podman-run.1.html | The OCI-aligned convention: "Additionally, a container environment file is created in each container to indicate to programs they are running in a container. This file is located at `/run/.containerenv` (or `/var/run/.containerenv` for FreeBSD containers)." The file is empty for rootless non-privileged containers and contains key-value pairs (engine, name, id, image) for `--privileged`. |
| Docker `/.dockerenv` convention | Long-standing Docker behavior; documented across Docker docs and reproduced by every container-detection helper (e.g., systemd's `detect_container_files()` enumerates `/.dockerenv` and `/run/.containerenv`). |
| Brendan Gregg, "AWS EC2 Virtualization 2017: Introducing Nitro" | https://www.brendangregg.com/blog/2017-11-29/aws-ec2-virtualization-2017.html | Documents that Nitro is "based on the KVM core kernel module" but presents itself via DMI as `Amazon EC2` (sys_vendor) — confirming that ventd must treat `Amazon EC2` as a valid VM signal even though kernel will detect KVM. |
| Proxmox VE LXC documentation | https://pve.proxmox.com/wiki/Linux_Container | Confirms that LXC containers "share the host's Linux kernel directly" and "CPU related information is not hidden from an LXC container" (Proxmox forum thread https://forum.proxmox.com/threads/lxc-container-show-load-and-hardware-from-host.26051/). This is the source of the DMI bleed-through pattern: `/sys/class/dmi/id/sys_vendor` inside the container shows the physical motherboard. |
| Xen detection issues in systemd (#22511, #6442, #2639, #28113) | https://github.com/systemd/systemd/issues/22511 and https://github.com/systemd/systemd/issues/6442 | Document the ordering bug class that ventd must avoid: `/sys/hypervisor/type` reports `xen` on **both** dom0 and domU; the only authoritative dom0 marker is `/sys/hypervisor/properties/features` bit `XENFEAT_dom0` being set (mask `0x6000`/`0x800` family of values). Also: `/proc/xen/capabilities` is more reliable than `/sys/hypervisor` because xenfs may not be mounted yet at boot. |

---

## 3. Comparative matrix

The matrix below uses these column codes:
- **Pure-Go**: Y = trivially file-read; P = needs CPUID via Go assembly (still CGO-free); N = requires CGO
- **FP-risk** (false-positive risk for "this is the actual environment"): L/M/H

### 3.1 Hypervisor / VM environments

| Environment | Primary signal | File path / source | Expected value | Secondary signals | FP risk | Pure-Go |
|---|---|---|---|---|---|---|
| **KVM (no virtio passthrough)** | CPUID hypervisor leaf 0x40000000 vendor | `/proc/cpuinfo` (look for `hypervisor` flag) + CPUID 0x40000000 | `KVMKVMKVM\0\0\0` | DMI `sys_vendor` = `QEMU` and/or `product_name` = `KVM`; `/sys/devices/virtual/dmi/id/product_name` containing `Standard PC (Q35 + ICH9, 2009)`; presence of `/dev/kvm` on host (irrelevant inside guest) | L | P (CPUID needs asm) / Y (cpuinfo flag is a plain file) |
| **KVM (with virtio passthrough or PCIe SR-IOV)** | CPUID + DMI as KVM | same as above | same | additional PCI devices in `/sys/bus/pci/devices` not matching virtio (`1af4:*`) — e.g., real Realtek NIC, Intel HEDT chipset bridge — indicate passthrough | L | Y |
| **QEMU (TCG / no -enable-kvm)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `QEMU` | `product_name` = `Standard PC (...)`; CPUID leaf-1 ECX bit-31 (`X86_FEATURE_HYPERVISOR`) **may be unset** — this is the canonical false-negative case | M | Y |
| **QEMU (KVM mode)** | CPUID `KVMKVMKVM` | CPUID 0x40000000 | `KVMKVMKVM` | DMI sys_vendor = `QEMU` | L | P |
| **VMware Workstation/ESXi/Fusion** | CPUID `VMwareVMware` | CPUID 0x40000000 | `VMwareVMware` | DMI sys_vendor matches `VMware, Inc.` (string `VMware` is sufficient prefix; `VMW` also seen on older ESX); `product_name` = `VMware Virtual Platform` or `VMware7,1` | L | P / Y |
| **Hyper-V (Windows Server, Azure)** | CPUID `Microsoft Hv` | CPUID 0x40000000 | `Microsoft Hv` | DMI sys_vendor = `Microsoft Corporation`; `product_name` = `Virtual Machine`; `/sys/hypervisor/type` may be empty; presence of `/sys/bus/vmbus` | L (but see Xen-cloaks-as-Hyper-V edge case below) | P / Y |
| **Xen PV (domU)** | `/proc/xen/capabilities` exists, content lacks `control_d` | `/proc/xen/capabilities` | empty / `(no string)` for domU | `/sys/hypervisor/type` = `xen`; CPUID `XenVMMXenVMM`; DMI sys_vendor = `Xen`, product_name = `HVM domU`; `XENFEAT_dom0` bit (in `/sys/hypervisor/properties/features`) is **NOT** set | L | Y |
| **Xen HVM (domU)** | DMI product_name | `/sys/class/dmi/id/product_name` | `HVM domU` | DMI sys_vendor = `Xen`; CPUID `XenVMMXenVMM`; `/proc/cpuinfo` `hypervisor` flag set | L | Y |
| **Xen dom0** | `/sys/hypervisor/properties/features` with `XENFEAT_dom0` bit set | `/sys/hypervisor/properties/features` | hex value with bit indicating `XENFEAT_dom0` (e.g., `00006805`, `000228f0`, `00002705` with dom0 bit) | `/proc/xen/capabilities` contains string `control_d`; DMI is the host's real motherboard | L | Y |
| **VirtualBox (Oracle)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `innotek GmbH` (legacy) or `Oracle Corporation`; `board_vendor` = `Oracle Corporation`; `product_name` = `VirtualBox` | CPUID `KVMKVMKVM` if VBox uses KVM acceleration on Linux host; chassis_vendor = `Oracle Corporation` | L | Y |
| **Parallels Desktop** | CPUID ` lrpepyh vr` (note leading space) | CPUID 0x40000000 | ` lrpepyh vr` | DMI sys_vendor = `Parallels Software International Inc.` or contains `Parallels` | L | P |
| **AWS EC2 Nitro** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `Amazon EC2` | CPUID `KVMKVMKVM` (Nitro is KVM-derived, per Brendan Gregg / AWS); `product_name` like `c5.large`, `m6i.xlarge`, etc.; `bios_vendor` = `Amazon EC2`; presence of `/sys/devices/virtual/dmi/id/board_asset_tag` with i-…-style asset tag | L | Y |
| **AWS EC2 (legacy Xen)** | DMI product_name | `/sys/class/dmi/id/product_name` | `HVM domU` | sys_vendor = `Xen`; `bios_version` contains `amazon` | L | Y |
| **GCP (Google Compute Engine)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `Google` or `Google Compute Engine` | CPUID `KVMKVMKVM`; product_name = `Google Compute Engine`; `bios_vendor` = `Google` | L | Y |
| **Microsoft Azure** | CPUID `Microsoft Hv` | CPUID 0x40000000 | `Microsoft Hv` | DMI sys_vendor = `Microsoft Corporation`; `chassis_asset_tag` = `7783-7084-3265-9085-8269-3286-77` (Azure-specific) | L | P / Y |
| **DigitalOcean (KVM)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `DigitalOcean` | product_name = `Droplet`; CPUID `KVMKVMKVM` | L | Y |
| **Bochs** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `Bochs` | CPUID typically blank | L | Y |

### 3.2 Container / namespace environments

| Environment | Primary signal | File path / source | Expected value | Secondary signals | FP risk | Pure-Go |
|---|---|---|---|---|---|---|
| **Docker (rootful, non-privileged)** | `/.dockerenv` exists | `/.dockerenv` | file present (typically empty) | `/proc/1/cgroup` contains `/docker/`; `/proc/self/mountinfo` shows overlayfs as `/`; `/proc/1/sched` PID != 1 in some runtimes | L | Y |
| **Docker (--privileged)** | `/.dockerenv` exists | `/.dockerenv` | file present | cgroup as above; **but** sysfs is read-write and DMI bleed-through occurs identically to LXC | L | Y |
| **Docker-in-Docker** | `/.dockerenv` AND nested `/proc/1/cgroup` | both exist; cgroup path contains multiple `/docker/<id>/docker/<id2>/` segments | — | mountinfo shows recursive overlayfs | M | Y |
| **Podman (rootful)** | `/run/.containerenv` exists | `/run/.containerenv` | file present, contents: `engine="podman-..."`, `rootless=0`, `name=...`, `id=...` | `/proc/1/cgroup` contains `libpod` or `machine.slice` | L | Y |
| **Podman (rootless)** | `/run/.containerenv` exists | `/run/.containerenv` | typically empty file; rootless containers may lack engine info | `/proc/self/uid_map` shows non-trivial mapping (UID 0 mapped to a non-zero host UID); `/proc/1/cgroup` under `user.slice/user-N.slice` | L | Y |
| **LXC (privileged)** | `/proc/1/environ` contains `container=lxc` | `/proc/1/environ` (NUL-separated) | `container=lxc\0` | `/dev/.lxc` directory or `/dev/.lxc-boot-id`; `/proc/1/cgroup` contains `/lxc/<name>` or `/lxc.payload.<name>`; mountinfo shows `lxcfs` mounts under `/proc/cpuinfo`, `/proc/meminfo`, `/proc/uptime`, `/proc/stat` | L | Y (need root to read /proc/1/environ; ventd typically runs as root for PWM access, so this is OK) |
| **LXC (unprivileged)** | same as above | same | `container=lxc` (set by lxc-start) | `/proc/self/uid_map` shows mapping `0 100000 65536` style range; cgroup path under `/user.slice/.../lxc.payload.<name>` | L | Y |
| **Proxmox LXC (canonical homelab case)** | `/proc/1/environ` `container=lxc` | `/proc/1/environ` | `container=lxc` | DMI **leaks the host motherboard** (sys_vendor = `ASUSTeK COMPUTER INC.`, `Supermicro`, etc.) — this is the bleed-through; cgroup path contains `/lxc/<vmid>` (e.g., `/lxc/100`); lxcfs-mounted `/proc/cpuinfo`, `/proc/meminfo`; `/sys/class/dmi/id/product_uuid` is the **host's** UUID | L | Y |
| **systemd-nspawn** | `/proc/1/environ` `container=systemd-nspawn` | `/proc/1/environ` | `container=systemd-nspawn` | `/run/host/container-manager` (newer hosts place this); `/run/systemd/container` may exist | L | Y |
| **Kubernetes pod (kubepods)** | `/proc/1/cgroup` contains `kubepods` | `/proc/1/cgroup` | path containing `/kubepods.slice/...` or legacy `/kubepods/...` | underlying runtime detected via `cri-containerd-`, `crio-`, or `docker-` segment in same path; `/.dockerenv` may also exist if Docker shim; `KUBERNETES_SERVICE_HOST` env (but ventd doesn't read env for detection) | L | Y |
| **WSL1** | `/proc/sys/kernel/osrelease` contains `Microsoft` | `/proc/sys/kernel/osrelease` | substring `Microsoft` (capital M historically) | `/proc/version` contains `Microsoft`; `/proc/cpuinfo` has no `hypervisor` flag (WSL1 is a syscall translation layer, not a VM); no `/sys/class/dmi` | L | Y |
| **WSL2** | `/proc/sys/kernel/osrelease` contains `microsoft` (lowercase) and/or `WSL2` | `/proc/sys/kernel/osrelease` | e.g., `5.10.16.3-microsoft-standard-WSL2+` or `6.10.0-...-microsoft-standard-WSL2+` (also exposed via `/proc/version`) | CPUID reports `Microsoft Hv` (because WSL2 IS a Hyper-V VM); `/proc/cpuinfo` `hypervisor` flag SET; no real DMI; `/run/WSL` path may exist | L | Y |
| **cgroup v2 unified hierarchy in any container** | `/proc/1/cgroup` shows single line `0::/...` | `/proc/1/cgroup` | `0::/<path>` — pattern-match `<path>` for `docker`/`kubepods`/`lxc`/`libpod`/`machine` substrings; the bare `0::/` (with empty path) inside many cgroup-v2-namespaced runtimes means "containerized but path hidden" — itself a strong container signal | L | Y |

---

## 4. Ranked precedence chain (the order ventd must check signals)

This is the canonical order; later checks only run if earlier checks return "unknown". The order intentionally puts container signals before VM signals because a **container-on-VM is always `Container`** for safety purposes — the container shares the kernel and `/sys/class/hwmon` is owned by the kernel which is owned by whatever scheduled the container, regardless of whether that kernel is itself in a VM.

```
Step 1.  /proc/1/environ            → "container=" prefix     → LXC | nspawn | docker | podman
Step 2.  /run/.containerenv          → exists                   → Podman (or any OCI runtime that adopts the convention)
Step 3.  /.dockerenv                 → exists                   → Docker (also covers Docker-in-Docker after cgroup parse)
Step 4.  /proc/1/cgroup              → substring scan           → kubepods | docker | lxc | libpod | machine.slice |
                                                                    cri-containerd | crio | systemd-nspawn
Step 5.  /proc/self/mountinfo        → overlay/lxcfs/fuse        → corroborate Step 4; detect lxcfs masking
Step 6.  /proc/sys/kernel/osrelease  → "microsoft" | "WSL"       → WSL1/WSL2  (CHECK BEFORE VM; WSL2 must classify as
                                       (case-insensitive)          WSL_CONTAINER, not Hyper-V VM)
Step 7.  /proc/xen/capabilities      → exists                   → Xen (then refine with XENFEAT_dom0)
Step 8.  /sys/hypervisor/properties/features → bit XENFEAT_dom0  → Xen dom0 if set; Xen domU if not
Step 9.  /sys/class/dmi/id/sys_vendor + /product_name            → systemd's DMI vendor table
Step 10. /proc/cpuinfo "hypervisor" flag                         → generic VM yes/no
Step 11. CPUID 0x40000000 vendor (via Go-asm if available, else
         skip — flag from Step 10 is sufficient signal of "VM")  → exact VM vendor
Step 12. /sys/firmware/dmi/entries/0-0/raw byte 0x13 bit 4       → SMBIOS "VM" bit (catches obfuscated-DMI VMs that
                                                                    still expose the SMBIOS-VM bit)
Step 13. /proc/cmdline                                            → corroborating: "console=ttyS0" + virtio drivers
                                                                    suggests VM; "intel_iommu=on" + IOMMU groups
                                                                    suggests bare metal or passthrough host
Step 14. (none of the above triggered)                            → BARE_METAL
```

### 4.1 Combination rules for ambiguous cases

These are the actual decision rules to encode in ventd:

| If signal A says… | And signal B says… | Then classification is… |
|---|---|---|
| Step 4 cgroup contains `kubepods` | DMI sys_vendor = `Amazon EC2` | `Kubernetes-on-EC2` → **CONTAINER takes precedence** for hwmon-write policy |
| Step 4 cgroup contains `lxc` | DMI sys_vendor = `ASUSTeK`/`Intel`/`Supermicro` (any non-virt vendor) | `Proxmox-style LXC with bleed-through` → **CONTAINER**; the DMI is the host's |
| Step 6 osrelease contains `microsoft` | CPUID = `Microsoft Hv` | `WSL2` (NOT pure Hyper-V) — WSL marker wins |
| Step 6 osrelease has no `microsoft` | CPUID = `Microsoft Hv` | `Hyper-V VM` |
| Step 7 `/proc/xen/capabilities` exists | Step 8 `XENFEAT_dom0` bit SET | `Xen dom0` (treat as bare-metal-equivalent for hwmon) |
| Step 7 `/proc/xen/capabilities` exists | Step 8 `XENFEAT_dom0` bit UNSET | `Xen domU` |
| DMI sys_vendor = `QEMU` | CPUID hypervisor flag UNSET | `QEMU TCG` (very slow software emulation; treat as VM for safety) |
| DMI sys_vendor = `QEMU` | CPUID = `KVMKVMKVM` | `KVM/QEMU` |
| DMI sys_vendor = `Xen`, product_name = `HVM domU` | CPUID = `Microsoft Hv` | `Xen cloaking as Hyper-V` (per systemd #8844) — DMI wins, classify as `Xen` |
| Step 1 `container=lxc` | Step 8 `/sys/hypervisor` reports `xen` | `LXC inside a Xen domU` → **CONTAINER** |
| `/.dockerenv` AND `/proc/1/cgroup` has nested `/docker/.../docker/...` | — | `Docker-in-Docker` → **CONTAINER** |
| All steps 1–13 fail | — | `BARE_METAL` |

---

## 5. Go-pseudocode decision tree (structure only)

This is the recommended skeleton. It is intentionally written so each probe is a separate, individually-testable function returning `(VirtClass, Confidence, []Evidence)` rather than a flat boolean — so ventd can log *why* it decided what it decided, which is essential for the homelab user filing a bug report from inside a Proxmox LXC.

```go
// Package detect — Tier-2 runtime classification for ventd.
// CGO_ENABLED=0; pure file-IO and Go-asm (no C deps).

type VirtClass int
const (
    VirtUnknown VirtClass = iota
    BareMetal
    // VM classes
    VMQemuTCG        // QEMU without KVM acceleration
    VMKVM            // generic KVM/QEMU-KVM
    VMVMware         // Workstation/ESXi/Fusion
    VMHyperV         // Hyper-V (Windows Server / Azure)
    VMXenPVDomU
    VMXenHVMDomU
    VMXenDom0        // privileged; hwmon ALLOWED
    VMVirtualBox
    VMParallels
    VMAmazonNitro    // KVM-derived; DMI says Amazon EC2
    VMAmazonXenHVM   // legacy EC2
    VMGCP
    VMAzure          // alias of HyperV with Azure asset tag
    VMDigitalOcean
    VMBochs
    VMOther          // SMBIOS VM bit set, vendor unknown
    // Container classes
    ContDocker
    ContDockerPrivileged
    ContDockerInDocker
    ContPodmanRootful
    ContPodmanRootless
    ContLXC
    ContLXCUnprivileged
    ContProxmoxLXC   // LXC with DMI bleed-through
    ContSystemdNspawn
    ContKubernetes   // kubepods cgroup, runtime-agnostic
    ContWSL1
    ContWSL2
    ContOther        // unclassified container
)

type Confidence int
const ( ConfLow Confidence = iota; ConfMedium; ConfHigh )

type Evidence struct {
    Source string  // e.g. "/proc/1/cgroup"
    Match  string  // matched substring
    Note   string  // human-readable
}

type Detection struct {
    Class      VirtClass
    Confidence Confidence
    Evidence   []Evidence
    HostVendor string // DMI sys_vendor, raw — useful for bleed-through reporting
    HostProduct string // DMI product_name, raw
    HypervisorVendor string // CPUID 0x40000000 result, if read
}

// Detect runs the full Tier-2 probe. Order matches §4.
func Detect() Detection {
    var ev []Evidence

    // --- Container tier (highest priority for hwmon safety) ---
    if c, e := probeProc1Environ(); c != VirtUnknown {           // Step 1
        return finalize(c, ConfHigh, append(ev, e...))
    }
    if c, e := probeContainerEnvFiles(); c != VirtUnknown {      // Steps 2 + 3
        // c may be ContPodman* or ContDocker; refine via cgroup
        if cc, ce := probeProc1Cgroup(); cc != VirtUnknown {
            return finalize(reconcile(c, cc), ConfHigh, append(ev, append(e, ce...)...))
        }
        return finalize(c, ConfHigh, append(ev, e...))
    }
    if c, e := probeProc1Cgroup(); c != VirtUnknown {            // Step 4
        // kubepods | docker | lxc | libpod | machine | systemd-nspawn
        // Refine LXC vs Proxmox-LXC via DMI bleed-through check
        if c == ContLXC {
            if dmi := readDMIVendor(); dmi != "" && !isVirtVendor(dmi) {
                c = ContProxmoxLXC
                e = append(e, Evidence{"/sys/class/dmi/id/sys_vendor", dmi,
                    "DMI bleed-through: container is exposing host motherboard"})
            }
        }
        return finalize(c, ConfHigh, append(ev, e...))
    }
    if c, e := probeMountinfoLXCFS(); c != VirtUnknown {         // Step 5
        return finalize(c, ConfMedium, append(ev, e...))
    }

    // --- WSL must be checked BEFORE Hyper-V (CPUID would say "Microsoft Hv") ---
    if c, e := probeWSLOSRelease(); c != VirtUnknown {           // Step 6
        return finalize(c, ConfHigh, append(ev, e...))
    }

    // --- VM tier ---
    if c, e := probeXen(); c != VirtUnknown {                    // Steps 7 + 8
        return finalize(c, ConfHigh, append(ev, e...))
    }
    if c, e := probeDMI(); c != VirtUnknown {                    // Step 9
        return finalize(c, ConfHigh, append(ev, e...))
    }
    hv := probeCPUInfoHypervisorFlag()                           // Step 10
    if hv {
        if c, e := probeCPUIDVendor(); c != VirtUnknown {        // Step 11
            return finalize(c, ConfHigh, append(ev, e...))
        }
        // Hypervisor flag set but vendor unknown
        return finalize(VMOther, ConfMedium, ev)
    }
    if probeSMBIOSVMBit() {                                      // Step 12
        return finalize(VMOther, ConfLow, ev)
    }
    _ = probeProcCmdline()                                       // Step 13 (corroborating only)

    return finalize(BareMetal, ConfHigh, ev)                     // Step 14
}

// PolicyForPWMWrite returns whether ventd's PWM writer should proceed,
// per the spec table in §6.
func (d Detection) PolicyForPWMWrite() Policy { ... }
```

Each probe (`probeProc1Environ`, `probeProc1Cgroup`, etc.) is a thin wrapper around `os.ReadFile` + `bytes.Contains` / regex — *no syscalls* beyond the file read, *no fork*, *no shell*. The only function that needs platform-specific assembly is `probeCPUIDVendor`, and ventd may legitimately omit it: the `hypervisor` flag from `/proc/cpuinfo` is sufficient to gate "is this a VM?", and the DMI vendor from Step 9 is sufficient to identify *which* VM. CPUID is a "nice-to-have" that improves classification confidence when DMI is obfuscated (e.g., a custom-DMI VMware guest that still leaks `VMwareVMware` in CPUID).

---

## 6. Edge cases and recommended handling

### 6.1 Proxmox LXC with DMI bleed-through (the canonical homelab case)

**Symptom:** Inside a Proxmox LXC container, `/sys/class/dmi/id/sys_vendor` returns `ASUSTeK COMPUTER INC.` (or whatever the host motherboard is). `/sys/class/dmi/id/product_uuid` is the host's UUID. `/proc/cpuinfo` shows the host's full CPU including all cores. `htop` and `top` report host load (per Proxmox forum thread on lxc-load-from-host).

**Why DMI alone is not enough:** ventd cannot distinguish "I am a bare-metal homelab on this ASUS motherboard" from "I am a Proxmox LXC container running on top of a bare-metal homelab on this ASUS motherboard" using DMI signals — they are byte-identical.

**Reliable detection:** the LXC tooling sets `container=lxc` in PID 1's environment (`/proc/1/environ`). It is also detectable via `/proc/1/cgroup` containing `/lxc/<vmid>` or `/lxc.payload.<vmid>` segments, and via `/proc/self/mountinfo` showing `lxcfs` mounts on top of `/proc/cpuinfo`, `/proc/meminfo`, `/proc/uptime`, `/proc/stat`, and `/proc/loadavg`.

**Recommended action:** ventd MUST classify this as `ContProxmoxLXC` and BLOCK PWM writes. The Proxmox host has its own fan governance (typically the BIOS, plus maybe `pwmconfig`/`fancontrol` on the PVE host); two writers fighting over the same `/sys/class/hwmon/hwmon*/pwm*` files is the worst-case behavior. If the user genuinely wants ventd to control fans from inside the container, they must (a) bind-mount `/sys/class/hwmon` read-write into the container, (b) configure cgroup device access to allow it, and (c) set the override flag `--allow-container-hwmon` documented below.

**Note on log message:** ventd should emit a specific, recognizable log line ("Tier-2 detected Proxmox-style LXC with DMI bleed-through to host vendor=`<vendor>` product=`<product>` — refusing PWM write, see --allow-container-hwmon"). Homelab users on Proxmox WILL hit this case, and the log message is the documentation.

### 6.2 WSL2 = Hyper-V under-the-hood

**Symptom:** `/proc/cpuinfo` `hypervisor` flag is set; CPUID 0x40000000 returns `Microsoft Hv`; DMI is fragmentary or absent (the WSL2 utility VM has minimal SMBIOS).

**Why this matters:** without the WSL-specific check, a naïve detector would classify WSL2 as `VMHyperV` and might ALLOW PWM writes (because Hyper-V VMs with passthrough are a legitimate scenario in Azure). But WSL2 NEVER has hwmon passthrough; `/sys/class/hwmon` inside WSL2 is either empty or contains synthetic devices that map to nothing physical.

**Reliable detection:** `/proc/sys/kernel/osrelease` contains the substring `microsoft` (lowercase on WSL2; `Microsoft` capitalized on WSL1) **and/or** `WSL2`. Per Microsoft WSL issue #6911 and #11814, the format is `<version>-microsoft-standard-WSL2+`. Use case-insensitive matching (the smallstep/cli #332 bug class is the warning here).

**Recommended action:** classify as `ContWSL2` (NOT `VMHyperV`); BLOCK unconditionally. There is no override flag for WSL — the user should not be running ventd in WSL.

### 6.3 Nested KVM (KVM-in-KVM)

**Symptom:** Both layers report the `hypervisor` flag and `KVMKVMKVM` CPUID. AWS bare-metal C8i/M8i/R8i nested instances are the canonical real-world case (AWS docs confirm KVM and Hyper-V as supported L1 hypervisors as of 2026).

**Why this matters:** ventd in the inner VM sees a `hypervisor` flag and DMI = `QEMU` or `KVM` or `Amazon EC2`, depending on the L1 hypervisor's configuration. Detection cannot reliably distinguish "single-layer VM" from "nested VM" from inside the inner guest, because the kernel only exposes the immediately-adjacent hypervisor (matching Lennart's "we only return the inner-most virtualization" design).

**Recommended action:** treat both layers identically — `VMKVM` with confidence Medium, BLOCK by default. This is correct because nested inner VMs almost never have real-hardware passthrough (it would defeat the purpose of nesting). Override flag `--allow-vm-pwm` (see §7) is the escape hatch for the rare passthrough case.

### 6.4 Docker-in-Docker

**Symptom:** Both `/.dockerenv` and a `/proc/1/cgroup` with two `docker` segments (`/docker/<outer>/docker/<inner>` in cgroup v1, or a `0::/...` v2 path that traversed two cgroup namespaces).

**Recommended action:** classify as `ContDockerInDocker`; BLOCK. Same as plain Docker — the DinD pattern is used for CI runners and never has hwmon access.

### 6.5 Cloud Nitro (DMI says Amazon EC2 but virt is KVM-derived)

**Symptom:** DMI sys_vendor = `Amazon EC2`; CPUID = `KVMKVMKVM`; `/proc/cpuinfo` has `hypervisor` flag.

**Why this is not a bug:** AWS Nitro is documented (Brendan Gregg blog, AWS Nitro page) as KVM-based but exposes `Amazon EC2` in DMI for cloud-vendor identification. systemd-detect-virt returns `amazon` for this case (manpage Table 1: "amazon — Amazon EC2 Nitro using Linux KVM").

**Recommended action:** classify as `VMAmazonNitro`; BLOCK. EC2 instances have no PWM. Even bare-metal EC2 instances (`*.metal`) do not expose Linux hwmon for the chassis fans — those are managed by the Nitro hardware controller out-of-band.

### 6.6 Containers running under VMs (the cgroup-precedence rule)

**Symptom:** `/proc/1/cgroup` contains `/kubepods.slice/...` AND DMI sys_vendor = `QEMU` or `Amazon EC2`.

**Recommended action:** Container precedence wins. Classify as `ContKubernetes`, NOT `VMKVM`. Reason: regardless of whether the underlying host is a VM or bare metal, the Kubernetes pod has a shared kernel with siblings on the same node, and `/sys/class/hwmon` inside the pod is almost certainly either absent (default case) or shared (only if the pod has `securityContext.privileged: true` and explicit hostPath mounts). BLOCK.

### 6.7 QEMU TCG without -enable-kvm and no DMI (false-negative)

**Symptom:** CPUID `hypervisor` flag UNSET (TCG does not expose it on older versions); DMI sys_vendor = `QEMU` is the only signal. systemd's `detect_vm_smbios()` falls back to reading `/sys/firmware/dmi/entries/0-0/raw` byte 0x13 bit 4 — the SMBIOS "system is virtual" bit. This is the "QEMU 3.1.0 (with or without KVM)" case explicitly called out in systemd virt.c comments.

**Recommended action:** ventd's Step 9 (DMI sys_vendor `QEMU`) catches this. The Step 12 SMBIOS-bit fallback catches the rarer case of fully-custom DMI (where sys_vendor was overridden via `-smbios type=1,manufacturer=Foobar` per the QEMU smbios mapping doc) but the SMBIOS VM bit was not also overridden. Classify as `VMQemuTCG` (or `VMOther` if only the SMBIOS bit triggered); BLOCK.

### 6.8 Obfuscated / anti-detection VMs

**Symptom:** A user has configured VMware with `monitor_control.disable_directexec = TRUE` and similar anti-detection knobs, AND has rewritten DMI strings via `smbios.reflectHost = TRUE` to mirror the host. CPUID's `hypervisor` flag *can* still be hidden in some configurations.

**Reality check:** This is malware-research / sandbox-evasion territory and is far outside ventd's threat model. ventd is not a security tool. If a user has gone to this much effort to hide a VM, they either (a) genuinely want ventd to run and have a working PWM passthrough (in which case `--allow-vm-pwm` is the path) or (b) are doing something ventd should not be involved in.

**Recommended action:** if all 13 probes return `BareMetal` but the user reports incorrect fan behavior, the spec should document the `--force-bare-metal=false` / `--probe-hwmon-write-test` diagnostic flag. ventd should NOT attempt heuristic anti-evasion.

### 6.9 minimalCons containers (BusyBox, scratch images)

**Symptom:** Container runtime did not set `container=` env; `/.dockerenv` may or may not exist (depending on runtime); cgroup v2 reports `0::/`.

**Recommended action:** `0::/` (the bare cgroup-v2 root with no path) inside a process whose `/proc/self/uid_map` shows non-trivial mapping is itself a strong container indicator. ventd should emit a Confidence=Medium `ContOther` classification and BLOCK. ONLYOFFICE issue #122 documents the cgroup-v2 detection failure mode that ventd must avoid.

### 6.10 Xen ordering hazard

**Symptom:** systemd issue #6442 documents that `detect_vm()` running before xenfs is mounted gives wrong dom0 result. ventd's probe runs at daemon startup, well after rootfs is mounted, so this is unlikely to bite — but the lesson is: do NOT cache the result globally at process start. Re-probe on demand or at reload-config time.

---

## 7. ventd Tier-2 policy: BLOCK / ALLOW / OVERRIDE

This is the recommended default policy table for v0.5.1. It maps each detected `VirtClass` to one of three actions:

- **BLOCK** — refuse PWM writes; log clearly; daemon may continue running in monitor-only / report-only mode
- **ALLOW** — proceed normally
- **OVERRIDE** — BLOCK by default, but allow the user to opt in via a CLI flag and/or config setting

### 7.1 Default policy

| Class | Default action | Override flag (if any) | Rationale |
|---|---|---|---|
| `BareMetal` | **ALLOW** | — | The intended target. Homelab tower, MiniPC, laptop. |
| `VMXenDom0` | **ALLOW** | — | dom0 owns the real hardware; this is bare-metal-equivalent. |
| `VMKVM`, `VMQemuTCG` | **OVERRIDE** | `--allow-vm-pwm` | Default BLOCK because most KVM guests have no real PWM. ALLOW with override for the legitimate PCIe/Super-IO-passthrough case (e.g., a TrueNAS VM on Proxmox with the LPC chipset passed through). |
| `VMVMware`, `VMVirtualBox`, `VMHyperV` (excluding WSL2), `VMParallels`, `VMBochs`, `VMOther` | **OVERRIDE** | `--allow-vm-pwm` | Same reasoning. |
| `VMAmazonNitro`, `VMAmazonXenHVM`, `VMGCP`, `VMAzure`, `VMDigitalOcean` | **BLOCK** (no override) | — | Cloud VMs categorically have no PWM. The override flag is intentionally NOT honored here, because a "yes, I really want to write PWM" user on EC2 is almost certainly running a misconfigured deployment that ventd should refuse to participate in. The flag `--cloud-pwm-i-know-what-i-am-doing` is **explicitly out of scope** for v0.5.1. |
| `VMXenPVDomU`, `VMXenHVMDomU` | **OVERRIDE** | `--allow-vm-pwm` | Same as KVM. |
| `ContDocker`, `ContPodmanRootful`, `ContPodmanRootless`, `ContSystemdNspawn`, `ContKubernetes`, `ContOther` | **OVERRIDE** | `--allow-container-hwmon` | Default BLOCK. Override allows the legitimate case where the operator has explicitly bind-mounted `/sys/class/hwmon` with `:rw` and configured cgroup device cap. |
| `ContDockerPrivileged` | **OVERRIDE** | `--allow-container-hwmon` | `--privileged` containers can write hwmon, but doing so from a sibling-fighting standpoint is dangerous; require explicit opt-in. |
| `ContDockerInDocker` | **BLOCK** (no override) | — | DinD is a CI pattern; never has hwmon. |
| `ContLXC`, `ContLXCUnprivileged` | **OVERRIDE** | `--allow-container-hwmon` | Generic LXC; may legitimately have hwmon bind-mounted. |
| `ContProxmoxLXC` | **OVERRIDE** | `--allow-container-hwmon` (with extra warning in logs) | The bleed-through case. The override is permitted because some homelabbers DO run ventd in a Proxmox LXC with hwmon bind-mounted intentionally — but the log message must explicitly call out "your Proxmox host's BIOS or pwmconfig may also be writing to these PWMs; expect contention." |
| `ContWSL1`, `ContWSL2` | **BLOCK** (no override) | — | WSL has no real hwmon. Period. |
| `VirtUnknown` (all 13 probes returned nothing meaningful) | **BLOCK** | `--force-bare-metal` | Conservative default. The override is the user's "I trust my system, just try" escape hatch. |

### 7.2 Override-flag semantics

- Flags must be settable both via CLI (`ventd --allow-vm-pwm`) and via config file (`allow_vm_pwm: true` in the v0.5.1 YAML).
- The flag name is **explicit about acknowledging risk** — `--allow-vm-pwm` (not `--enable-vm`) makes it clear that the user is consenting to a class of behavior, not just turning a feature on.
- Flag overrides MUST be logged at INFO level with the detected class so audit trails work: `Tier-2 classification=VMKVM, override=--allow-vm-pwm honored, proceeding with PWM init`.
- A second-line defense: even with override flags, ventd should perform a **Tier-3 hwmon write-back probe** (write the current value, read back, confirm) before accepting that a given PWM is truly writable. This is out of scope for R1 but should be cross-referenced in the spec.

---

## 8. Pure-Go library survey (for vendoring decisions)

| Library | License | CGO status | Coverage vs. ventd needs | Recommendation |
|---|---|---|---|---|
| `github.com/shirou/gopsutil/v4/host` (`Virtualization()`) | BSD-3-Clause | CGO-free on Linux (only `host_darwin_cgo.go` uses CGO and is excluded with `CGO_ENABLED=0`); README states "All works are implemented without cgo by porting C structs to Go structs" | Returns `(system, role)` — covers Docker, LXC, KVM, Xen, VMware, Hyper-V, OpenVZ. Does NOT distinguish dom0/domU; does NOT detect Proxmox-LXC bleed-through (only reports `lxc`); does NOT special-case WSL2. Uses `/proc/cpuinfo`, `/proc/1/cgroup`, `/proc/xen`, DMI vendor strings. | **Conditionally usable as a baseline / cross-check**. Do NOT rely on it as the sole detector — its taxonomy is too coarse for ventd's BLOCK/ALLOW/OVERRIDE policy. Vendoring it adds a non-trivial dependency tree (`golang.org/x/sys`, internal/common, etc.) for a function we'd be wrapping in a richer probe anyway. **Recommendation: do not vendor; reimplement the ~300 LOC probe in ventd's own `internal/detect` package, citing gopsutil as inspiration.** |
| `github.com/zcalusic/sysinfo` | MIT | CGO-free (uses Go-assembly CPUID via `zcalusic/sysinfo/cpuid` subpackage) | Detects KVM, QEMU, Hyper-V, Parallels, VMware, Xen-HVM, Xen-PV via CPUID + `/sys/hypervisor/type`. Does not handle containers at all. | Useful as a **CPUID assembly reference** — the `cpuid.CPUID(&info, 0x1)` and 0x40000000 idiom is exactly what ventd needs if it implements the optional CPUID step (Step 11 in §4). Library itself is too narrow to use whole. **Recommendation: do not vendor; copy the ~30 LOC CPUID detection logic with attribution.** |
| `github.com/AkihiroSuda/go-detect-vm` | (search did not surface a current canonical repo at this URL — it appears the user's task description may have referenced a non-existent or moved project) | — | — | **Disqualified — cannot verify it exists in maintained form.** Skip. |
| Kubernetes `pkg/util/procfs` and downward-API helpers | Apache-2.0 | Pure-Go | Kubelet itself uses `/proc/self/cgroup` parsing for pod identification (kubernetes/kubernetes#95488 and related). Logic is embedded in kubelet, not exposed as a standalone helper. | **Recommendation: do not vendor kubelet code; the cgroup-substring-match logic is ~10 LOC and ventd should own it.** |
| `github.com/digitalocean/go-smbios` | Apache-2.0 | Pure-Go | Reads SMBIOS entries from `/sys/firmware/dmi/entries/`. Useful for the Step 12 SMBIOS-VM-bit probe (the obfuscated-DMI fallback). | **Recommendation: optional vendor for Step 12 only.** Light dependency, well-maintained, useful for the rare obfuscated-DMI case. |

**Summary recommendation:** ventd should not depend on any of these as a hard requirement. The total Pure-Go LOC needed for the full Tier-2 probe is small (~400 LOC including tests), all of it is direct file IO + string matching, and owning it in `internal/detect/` makes the test matrix (HIL validation against Proxmox, MiniPC, 13900K dual-boot, Steam Deck) much easier to maintain than wrestling with upstream library versions. systemd's `virt.c` is the architectural reference, not a vendored dependency.

---

## 9. Specific behaviors to encode in tests

The HIL (hardware-in-the-loop) test plan implied by the user's flag should exercise:

1. **Proxmox host** running detection → expect `BareMetal` (or `VMXenDom0` if Proxmox is on Xen, which is rare but possible).
2. **Proxmox + KVM guest** running detection → expect `VMKVM` with DMI `QEMU` and CPUID `KVMKVMKVM`.
3. **Proxmox + LXC guest (privileged)** running detection → expect `ContProxmoxLXC` with bleed-through evidence captured.
4. **Proxmox + LXC guest (unprivileged)** running detection → expect `ContProxmoxLXC` (or `ContLXCUnprivileged`) with `/proc/self/uid_map` evidence.
5. **MiniPC bare-metal Linux** → expect `BareMetal`, DMI vendor matches the actual MiniPC vendor (Beelink, Minisforum, Intel NUC, etc.); none of those should match systemd's virt-vendor table.
6. **13900K dual-boot host running native Linux** → expect `BareMetal`.
7. **13900K dual-boot host running WSL2** → expect `ContWSL2`; CPUID will say `Microsoft Hv` but osrelease takes precedence.
8. **Steam Deck (SteamOS 3.x, Arch-based)** → expect `BareMetal`. The Steam Deck has DMI `Valve` / product `Jupiter` (LCD) or `Galileo` (OLED), neither of which is in systemd's virt vendor table — so this is a useful "non-virt baseline" exactly as the user noted.
9. **Docker container on the 13900K** (rootful, non-privileged, default sysfs ro) → expect `ContDocker`, BLOCK.
10. **Docker container with `-v /sys/class/hwmon:/sys/class/hwmon` AND `--privileged`** → expect `ContDockerPrivileged`; BLOCK by default; ALLOW only if `--allow-container-hwmon` is set.

Each test case should assert both the `VirtClass` AND the `Evidence` slice — the latter is what ventd's logs surface to homelab users when something goes wrong, and locking in expected log substrings prevents regressions where a refactor accidentally changes the user-visible diagnostic.

---

## ARTIFACT 2 — Spec-ready findings appendix

### R1 — Tier-2 Detection Signal Reliability (Virtualization + Containers)

- **Defensible default(s):** `/proc/1/environ (container=) → /run/.containerenv → /.dockerenv → /proc/1/cgroup (kubepods|docker|lxc|libpod|machine|nspawn|cri-) → /proc/self/mountinfo (lxcfs/overlay) → /proc/sys/kernel/osrelease (microsoft|WSL — case-insensitive) → /proc/xen/capabilities → /sys/hypervisor/properties/features (XENFEAT_dom0 bit) → /sys/class/dmi/id/sys_vendor + /sys/class/dmi/id/product_name (systemd-canonical vendor table: QEMU, VMware/VMW, innotek GmbH/VirtualBox/Oracle Corporation, Xen, Bochs, Parallels, BHYVE, Hyper-V, Microsoft Corporation, Apple Virtualization, Google Compute Engine, Amazon EC2, DigitalOcean) → /proc/cpuinfo "hypervisor" flag → CPUID 0x40000000 vendor (KVMKVMKVM, VMwareVMware, Microsoft Hv, XenVMMXenVMM, " lrpepyh vr", "bhyve bhyve ", QNXQVMBSQG) → /sys/firmware/dmi/entries/0-0/raw byte 0x13 bit 4 (SMBIOS VM bit) → /proc/cmdline (corroborating only) → BareMetal default.` Policy mapping: BareMetal/Xen-dom0 → ALLOW; cloud VMs (Amazon EC2/GCP/Azure/DO) → BLOCK with no override; WSL1/WSL2/DinD → BLOCK with no override; all other VMs → BLOCK with `--allow-vm-pwm` override; all other containers (Docker/Podman/LXC/Proxmox-LXC/nspawn/k8s) → BLOCK with `--allow-container-hwmon` override; VirtUnknown → BLOCK with `--force-bare-metal` escape hatch.
- **Citation(s):**
  1. systemd `src/basic/virt.c` (canonical signal list + DMI vendor table + CPUID strings + WSL osrelease check + XENFEAT_dom0 logic): https://github.com/systemd/systemd/blob/main/src/basic/virt.c
  2. Linux kernel `arch/x86/kernel/cpu/hypervisor.c` (kernel's own probe order Xen-PV → Xen-HVM → VMware → Hyper-V → KVM → Jailhouse → ACRN; source of `/proc/cpuinfo` `hypervisor` flag): https://github.com/torvalds/linux/blob/master/arch/x86/kernel/cpu/hypervisor.c
  3. virt-what (Red Hat / RWMJ; documents the heuristic-based approach and the explicit warning that detection-by-virt-type is usually the wrong abstraction — feature-detection is preferred): https://people.redhat.com/~rjones/virt-what/
  4. systemd-detect-virt(1) manpage (canonical taxonomy + WSL-as-container rule): https://man7.org/linux/man-pages/man1/systemd-detect-virt.1.html
- **Reasoning summary:** Container signals (env/containerenv/cgroup) precede VM signals (DMI/CPUID) because a container always implies a shared kernel and unknown ownership of `/sys/class/hwmon`, regardless of the VM layer below; this matches systemd's design rule that "if both machine and container virtualization are used in conjunction, only the latter will be identified". WSL2 is checked before Hyper-V because its CPUID legitimately reports `Microsoft Hv` despite the OS-release marker being the only safe identifier of WSL's container-like semantics. DMI sys_vendor is checked before CPUID because the cloud cases (`Amazon EC2`, `Google Compute Engine`, `DigitalOcean`) are KVM-derived and would otherwise be misclassified as generic KVM, losing the cloud-specific BLOCK-no-override policy. The Xen ordering (`/proc/xen/capabilities` + `XENFEAT_dom0` bit before generic DMI) is required to correctly distinguish dom0 (ALLOW, owns hardware) from domU (OVERRIDE-required), and is the exact ordering bug that systemd has hit and fixed multiple times (issues #6442, #22511, #28113).
- **HIL-validation flag:** **Yes.** Required HIL matrix: (a) Proxmox host runs detection bare-metal → expect `BareMetal`; (b) Proxmox + KVM guest → expect `VMKVM` (override required); (c) Proxmox + privileged LXC → expect `ContProxmoxLXC` with bleed-through evidence; (d) Proxmox + unprivileged LXC → expect `ContLXCUnprivileged` or `ContProxmoxLXC` with uid_map evidence; (e) MiniPC running bare-metal Linux → expect `BareMetal` (DMI vendor confirms non-virt); (f) 13900K dual-boot Linux → expect `BareMetal`; (g) 13900K Windows + WSL2 → expect `ContWSL2` (osrelease overrides Hyper-V CPUID); (h) Steam Deck (SteamOS 3.x) → expect `BareMetal` baseline (DMI = `Valve` / `Jupiter` or `Galileo`, neither in virt-vendor table); (i) Docker on 13900K rootful default → expect `ContDocker` BLOCK; (j) Docker `--privileged -v /sys/class/hwmon:/sys/class/hwmon` → expect `ContDockerPrivileged` BLOCK without override, ALLOW with `--allow-container-hwmon`. Each test asserts both `VirtClass` and `Evidence[]` content.
- **Confidence:** **High** for the precedence-chain design and the expected signal values per environment (these are byte-identical to what systemd, the kernel, virt-what, gopsutil, and zcalusic/sysinfo independently agree on across multiple authoritative sources). **Medium** for the policy-table BLOCK/ALLOW/OVERRIDE choices — these reflect a defensible default for ventd's homelab/NAS/desktop audience, but the override-flag naming (`--allow-vm-pwm`, `--allow-container-hwmon`, `--force-bare-metal`) is a UX choice that should be re-examined during v0.5.1 implementation review. **Low** confidence is reserved for the SMBIOS-VM-bit fallback (Step 12) because raw SMBIOS parsing varies across kernel configs (Ubuntu's CONFIG_DMI_SYSFS-as-module bug, launchpad #2045561, demonstrates that `/sys/firmware/dmi/entries/0-0/raw` may be missing on minimal kernels) — ventd should treat this step as best-effort and not panic if the path is absent.
- **Spec ingestion target:** `spec-v0_5_1-catalog-less-probe.md` (Tier-2 detection section). Specifically, this R1 output should populate three sub-sections of that spec: (1) "Probe order and precedence" → §4 of this document; (2) "Detection→Policy mapping table" → §7.1 of this document; (3) "Edge cases and operator-facing log messages" → §6 of this document. The Go-pseudocode in §5 is intended for the implementation guide that accompanies the spec, not the spec itself.