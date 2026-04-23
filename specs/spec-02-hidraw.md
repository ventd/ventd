# spec-02-hidraw — Pure-Go hidraw substrate

**Status:** draft
**Supersedes in part:** spec-02 PR 1 (replaces `discover_linux.go` go-hid implementation)
**Author:** Phoenix (PhoenixDnB)
**Date:** 2026-04-24

## Motivation

spec-02 PR 1 shipped `usbbase.Device` backed by `github.com/sstallion/go-hid`,
which requires `CGO_ENABLED=1` via hidapi. ventd's release binary is
`CGO_ENABLED=0` — a load-bearing property, not marketing:

- Drops into scratch containers without glibc version coupling
- Single-file installs on TrueNAS/Unraid/Proxmox package wrappers
- NVML backend already proves the pattern (purego + dlopen, spec-00)

Accepting cgo for USB HID would split the release matrix permanently
(`ventd` vs `ventd-full`) and erode the constraint for every subsequent
USB backend (NZXT spec-02b, Lian Li spec-02c, Aquacomputer). The wedge
is one-way: once relaxed it is not restored.

This spec replaces the go-hid dependency with a pure-Go implementation
of the subset of Linux hidraw required by ventd USB backends. Corsair
(spec-02) and all future USB-HID backends build on this substrate.

## Scope

### In scope
- `internal/hal/usbbase/hidraw/` — Linux hidraw package, pure Go, no cgo
- Enumeration via `/sys/class/hidraw/` walk + sysfs uevent parsing
- Feature report get/set (variable length)
- Output report write, input report read
- Hotplug via `NETLINK_KOBJECT_UEVENT`
- Architecture coverage: amd64, arm64 (tested in CI)
- Replacement of `internal/hal/usbbase/discover_linux.go` go-hid calls
  with hidraw package calls
- Removal of `github.com/sstallion/go-hid` from `go.mod`

### Out of scope
- Windows HID (post-v1.0; separate subproject)
- macOS HID (not a target platform)
- Bluetooth HID (not needed for any planned backend; hidraw exposes it but
  we filter to `BUS_USB` at enumeration)
- Generic HID report descriptor parsing (backends hardcode their VID/PID
  protocol shape; descriptor parsing is a rabbit hole we do not need)
- 32-bit arm, riscv64, ppc64le, s390x (not in target matrix; revisit if
  a user reports a homelab install on one of these)
- Asynchronous interrupt read with epoll (request-response protocols do
  not need it; blocking read with deadline is sufficient)

## Invariant bindings

Bindings land in `.claude/rules/hidraw-safety.md`. 1:1 subtest mapping
enforced by tools/rulelint.

- **RULE-HIDRAW-01** — enumeration MUST filter to `BUS_USB` (0x03).
  Bluetooth and virtual buses are ignored even if they appear in
  `/sys/class/hidraw/`. Binds to `TestEnumerate_FiltersNonUSBBuses`.

- **RULE-HIDRAW-02** — a `Device` opened for read-only access MUST NOT
  accept `SendFeature` or `Write` calls. Attempting either returns
  `ErrReadOnly`. Binds to `TestDevice_ReadOnlyRejectsWrite`.

- **RULE-HIDRAW-03** — `Read` MUST respect the deadline set via
  `SetReadDeadline`. A blocked read woken by deadline returns
  `ErrTimeout`, not a partial buffer. Binds to
  `TestDevice_ReadDeadlineReturnsTimeout`.

- **RULE-HIDRAW-04** — `Close` is idempotent. Second and subsequent
  calls return nil, not EBADF or "already closed". Binds to
  `TestDevice_CloseIdempotent`.

- **RULE-HIDRAW-05** — the netlink uevent watcher MUST terminate within
  100ms of context cancellation. No leaked goroutines. Enforced by
  `goleak.VerifyNone` in `TestWatch_ContextCancelTerminates`.

- **RULE-HIDRAW-06** — ioctl number constants MUST be compile-time
  verified against the kernel uapi layout via `unsafe.Sizeof` asserts
  in `init_test.go`. If `struct hidraw_devinfo` changes size between
  architectures we target, the test fails before any syscall runs.
  Binds to `TestIoctlNumbers_MatchKernelUAPI`.

## Package shape

```
internal/hal/usbbase/hidraw/
├── hidraw.go              // Device, DeviceInfo, errors, doc comments
├── hidraw_linux.go        // Linux implementation wiring (//go:build linux)
├── hidraw_stub.go         // non-Linux stub returning ErrUnsupported
├── ioctl_linux.go         // ioctl number constants, _IOC encoding
├── enumerate_linux.go     // /sys/class/hidraw walker, uevent parser
├── netlink_linux.go       // NETLINK_KOBJECT_UEVENT watcher
├── device_linux.go        // Device struct, Read/Write/SendFeature/GetFeature
├── init_test.go           // compile-time size/layout asserts
├── enumerate_test.go      // uses synthetic sysfs fixture under t.TempDir()
├── netlink_test.go        // socketpair-based fake netlink
└── device_test.go         // Unix pipe pair as fake hidraw fd
```

No build tag named `hidraw`. Linux-vs-stub split is `//go:build linux` /
`//go:build !linux`. The backend is unconditionally available on Linux.

## Public API

Minimal surface. `usbbase.Device` continues to be the HAL-facing interface
from PR 1; the hidraw package implements it.

```go
package hidraw

// DeviceInfo describes a hidraw device discovered via sysfs.
type DeviceInfo struct {
    Path            string // /dev/hidrawN
    VendorID        uint16
    ProductID       uint16
    InterfaceNumber int    // -1 if unavailable
    SerialNumber    string // "" if device does not expose one
    BusType         uint32 // BUS_USB, BUS_BLUETOOTH, BUS_VIRTUAL, ...
}

// Enumerate returns all hidraw devices matching any of the given matchers.
// Devices with BusType != BUS_USB are excluded per RULE-HIDRAW-01.
func Enumerate(matchers []Matcher) ([]DeviceInfo, error)

// Open opens /dev/hidrawN for read-write access.
func Open(path string) (*Device, error)

// OpenReadOnly opens /dev/hidrawN for read-only access. Write and
// SendFeature return ErrReadOnly. Used by "unknown firmware" mode.
func OpenReadOnly(path string) (*Device, error)

// Watch emits add/remove events via netlink uevent. Terminates on
// ctx.Done() within 100ms (RULE-HIDRAW-05).
func Watch(ctx context.Context, matchers []Matcher) (<-chan usbbase.Event, error)

// Device is a single open hidraw handle.
type Device struct{ /* unexported */ }

func (d *Device) Info() DeviceInfo
func (d *Device) SendFeature(report []byte) error
func (d *Device) GetFeature(reportID byte, buf []byte) (int, error)
func (d *Device) Write(report []byte) (int, error)
func (d *Device) Read(buf []byte) (int, error)
func (d *Device) SetReadDeadline(t time.Time) error
func (d *Device) Close() error

var (
    ErrUnsupported = errors.New("hidraw not supported on this OS")
    ErrReadOnly    = errors.New("device opened read-only")
    ErrTimeout     = errors.New("read deadline exceeded")
)
```

## ioctl numbers — source of truth

Copied verbatim from `include/uapi/linux/hidraw.h`:

```
HIDIOCGRDESCSIZE    _IOR('H', 0x01, int)
HIDIOCGRDESC        _IOR('H', 0x02, struct hidraw_report_descriptor)
HIDIOCGRAWINFO      _IOR('H', 0x03, struct hidraw_devinfo)
HIDIOCGRAWNAME(len) _IOC(_IOC_READ, 'H', 0x04, len)
HIDIOCGRAWPHYS(len) _IOC(_IOC_READ, 'H', 0x05, len)
HIDIOCSFEATURE(len) _IOC(_IOC_WRITE|_IOC_READ, 'H', 0x06, len)
HIDIOCGFEATURE(len) _IOC(_IOC_WRITE|_IOC_READ, 'H', 0x07, len)
HIDIOCGRAWUNIQ(len) _IOC(_IOC_READ, 'H', 0x08, len)
```

`_IOC` encoding (asm-generic/ioctl.h — used by amd64 and arm64, verified
below):

```
_IOC(dir, type, nr, size) =
    (dir  << 30) |
    (size << 16) |
    (type <<  8) |
    (nr   <<  0)
```

with `_IOC_READ = 2`, `_IOC_WRITE = 1`, `_IOC_WRITE|_IOC_READ = 3`.

`golang.org/x/sys/unix` already exposes:
- `IoctlHIDGetDesc(fd, *HIDRawReportDescriptor)` — wraps HIDIOCGRDESC
- `IoctlHIDGetRawName(fd) (string, error)`
- `IoctlHIDGetRawPhys(fd) (string, error)`
- `IoctlHIDGetRawUniq(fd) (string, error)`

We use those. The three we hand-roll:
- `HIDIOCGRAWINFO` — fixed size, `struct hidraw_devinfo` (10 bytes: u32 + s16 + s16 + 2 bytes pad on amd64/arm64 natural alignment — see layout note)
- `HIDIOCSFEATURE(len)` — variable length, constructed at call time
- `HIDIOCGFEATURE(len)` — variable length, constructed at call time

**Layout note on `hidraw_devinfo`:** the kernel struct is `__u32 bustype; __s16 vendor; __s16 product;` — 8 bytes total, no padding on any arch we target. Go must match with `struct{ bustype uint32; vendor int16; product int16 }`. `init_test.go` asserts `unsafe.Sizeof == 8` (RULE-HIDRAW-06).

## Enumeration — sysfs walk

hidraw itself does not support enumeration. `/sys/class/hidraw/` is the
entry point:

```
/sys/class/hidraw/
├── hidraw0 -> ../../devices/pci.../usb.../hidraw/hidraw0
├── hidraw1 -> ...
```

Each `hidrawN` symlink resolves into the device tree. The parser reads:

- `/sys/class/hidraw/hidrawN/device/uevent` — contains `HID_ID=0003:00001B1C:00000C32` (bustype:VID:PID hex, zero-padded). Parse with `fmt.Sscanf` on the line starting `HID_ID=`.
- `/sys/class/hidraw/hidrawN/device/../bInterfaceNumber` — USB interface number as ASCII hex. Missing for non-USB devices; field then set to -1. (The `device` symlink points to the HID interface; its parent is the USB interface. Walk up one level.)
- `/sys/class/hidraw/hidrawN/device/../../serial` — serial number as plain string, if exposed by the USB descriptor. May be absent.

The device node itself is `/dev/hidrawN` where N matches the sysfs name. Permission: root by default; ventd's systemd unit will carry the necessary device ACLs (spec-02 deployment PR).

**Why sysfs not libudev:** libudev would require cgo or a pure-Go reimplementation of the udev protocol. Direct sysfs reads are stable, documented, and all we need. `github.com/pilebones/go-udev` exists as a reference for the netlink side but we do not take it as a dep — the netlink surface we need is ~80 LOC and vendoring a dep for that is not worth the supply chain cost.

## Hotplug — netlink

`socket(AF_NETLINK, SOCK_RAW, NETLINK_KOBJECT_UEVENT)`. Messages are
NUL-separated key=value ASCII. The first token before `\0` has shape
`<action>@<devpath>` where action is `add`, `remove`, `change`, etc.
Filter on `SUBSYSTEM=hidraw`.

Full parsing cost is ~40 LOC. `golang.org/x/sys/unix` exposes
`NETLINK_KOBJECT_UEVENT`, `AF_NETLINK`, `SOCK_RAW`, `Bind`, `Recvmsg`.

**Testability:** `Watch` takes a `netlinkSocket` interface (unexported)
with a `Recvmsg` method. Production impl wraps the real socket; test
impl is a `net.Pipe`-style in-memory feed. This lets `netlink_test.go`
drive synthetic uevents without a real netlink socket, which is needed
because test containers (GitHub Actions runners) have no netlink access.

## Migration plan — replacing PR 1's go-hid

Current state (main, post-PR 1):
- `internal/hal/usbbase/usbbase.go` — interface, OK
- `internal/hal/usbbase/discover_linux.go` — uses go-hid, REPLACED
- `internal/hal/usbbase/discover_stub.go` — ErrUnsupported, OK
- `internal/testfixture/fakehid/*` — implements usbbase.Device directly, OK
- `go.mod` — contains `github.com/sstallion/go-hid`, REMOVED

PR 1.5 changes:
1. Create `internal/hal/usbbase/hidraw/` package per shape above.
2. Rewrite `discover_linux.go` to call `hidraw.Enumerate` and
   `hidraw.Watch` instead of go-hid.
3. Remove `github.com/sstallion/go-hid` from `go.mod`; `go mod tidy`.
4. Verify `CGO_ENABLED=0 go build ./...` succeeds with no warnings.
5. Verify `go test -race ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...`.

Behavioural compatibility: `usbbase.Device` interface unchanged. fakehid
fixture unchanged. PR 2 (Corsair) proceeds unmodified on the new
substrate.

## Success criteria

- [ ] `CGO_ENABLED=0 go build -o /tmp/ventd ./cmd/ventd` succeeds
- [ ] `ldd /tmp/ventd` reports "not a dynamic executable"
- [ ] `grep -r hid go.mod go.sum` returns only internal imports (no go-hid, no hidapi)
- [ ] All 6 RULE-HIDRAW-* subtests pass
- [ ] `go test -race` clean on both amd64 and arm64 runners
- [ ] `go vet ./...` clean
- [ ] goleak detects zero leaked goroutines across all tests

## Failure modes

- **Kernel older than 2.6.31** — hidraw was added in 2.6.24, stabilised at 2.6.31. All target distros (TrueNAS SCALE, Unraid 7+, Proxmox 8+) ship kernels 6.x+. Document 4.x minimum in README if asked; do not gate at runtime.
- **hidraw device node missing** — user has not loaded hidraw module, or udev rules strip access. `Enumerate` returns nil slice, not an error. Log at info level with the `/sys/class/hidraw/` path being scanned so the operator sees "nothing found here" without panicking.
- **SELinux/AppArmor denies `/dev/hidrawN` open** — returns EACCES. Surface as `fs.PathError` from `Open`; backends translate to a backend-level degraded-status error. No special handling in the hidraw package.
- **Netlink socket permission denied** — happens inside user-namespaced containers. `Watch` returns a wrapped error on construction; caller falls back to periodic `Enumerate` polling. This fallback lives in spec-02 PR 2+, not here.
- **Device yanked mid-read** — `read(2)` returns ENODEV. `Device.Read` translates to `ErrDeviceGone`. Caller's responsibility to trigger re-enumeration.

## Non-failure modes we explicitly do not handle

- Concurrent `Read` from multiple goroutines on the same Device. Not
  supported. Upstream backends serialise. This is a contract, not a
  runtime check — documented in the Device godoc.
- Devices that require USB Set Configuration / Select Interface calls
  not covered by hidraw's default behaviour. None of ventd's planned
  backends hit this; if one does, it is a new spec.

## Budget

- Spec-writing time: covered by this chat (flat-rate)
- CC implementation: Sonnet only. Estimate $15–25 for the package +
  migration of `discover_linux.go` + tests. Single session, no
  subagents.
- Review/debug buffer: +$5. Hard cap: $30.

## Open questions resolved inline

- Q: Is there a good pure-Go reference for netlink uevent we can crib from?
  A: Yes — `github.com/pilebones/go-udev` (MIT). Do not take as a dep;
  cite in comments.
- Q: Does x/sys/unix already expose hidraw ioctls?
  A: Partially. `IoctlHIDGetDesc`, `IoctlHIDGetRaw{Name,Phys,Uniq}` yes.
  GetRawInfo and Get/SetFeature we build ourselves.
- Q: Do we need the report descriptor at all?
  A: No. Each backend knows its protocol a priori. Descriptor parsing
  is out of scope.
