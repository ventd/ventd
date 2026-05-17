# hidraw-safety — invariant bindings for pure-Go Linux hidraw substrate

**Package:** `internal/hal/usbbase/hidraw/`
**Spec:** `specs/spec-02-hidraw.md`
**Enforced by:** `tools/rulelint`

Every rule below is 1:1 with a subtest in the hidraw package. rulelint
fails the build if a rule lacks a corresponding subtest or if a subtest
drifts from its bound rule.

---

## RULE-HIDRAW-01 — enumeration MUST filter to USB bus

`Enumerate` MUST return only devices where the `HID_ID=` field in
`/sys/class/hidraw/hidrawN/device/uevent` begins with bustype `0003`
(BUS_USB). Bluetooth HID (`0005`), virtual (`0006`), and any other bus
are silently excluded.

**Rationale:** ventd's USB backends assume USB-specific descriptor
semantics (interface numbers, serial strings). A Bluetooth HID device
matching a target VID/PID by coincidence would fail opaquely at the
backend layer.

**Binds to:** `TestEnumerate_FiltersNonUSBBuses`

---

## RULE-HIDRAW-02 — read-only handle MUST reject writes

A `Device` opened via `OpenReadOnly` MUST return `ErrReadOnly` from
`SendFeature` and `Write` with no side effects on the underlying fd.
`GetFeature` and `Read` proceed normally.

**Rationale:** firmware-gated read-only mode (spec-02 RULE-LIQUID-03)
depends on this substrate guarantee. A silent passthrough would defeat
the compile-time split inside `internal/hal/liquid/corsair/`.

**Binds to:** `TestDevice_ReadOnlyRejectsWrite`

---

## RULE-HIDRAW-03 — Read MUST respect deadline

A `Read` call on a `Device` with a deadline set via `SetReadDeadline`
MUST return `ErrTimeout` (wrapping `os.ErrDeadlineExceeded`) when the
deadline passes before data arrives. The returned buffer MUST NOT
contain a partially-filled response.

**Rationale:** Corsair's stale-response discard loop (framing review
§5) must terminate; an untimed read on a wedged device hangs the
daemon indefinitely.

**Binds to:** `TestDevice_ReadDeadlineReturnsTimeout`

---

## RULE-HIDRAW-04 — Close MUST be idempotent

`Close` MUST return nil on the second and all subsequent calls. It
MUST NOT return EBADF, `fs.ErrClosed`, or "already closed" from the
public API even though the underlying syscall would.

**Rationale:** deferred-close patterns in higher layers close
opportunistically on the error path; a non-idempotent Close turns
error-path cleanup into a source of spurious error reports.

**Binds to:** `TestDevice_CloseIdempotent`

---

## RULE-HIDRAW-05 — netlink watcher MUST terminate on context cancel

`Watch` MUST return, close its internal netlink socket, and terminate
all spawned goroutines within 100ms of `ctx.Done()`. `goleak.VerifyNone`
MUST pass at the end of any test that invokes `Watch`.

**Rationale:** ventd's controller reloads the HAL on SIGHUP; leaked
netlink goroutines accumulate across reloads and eventually exhaust fd
limits on long-running daemons.

**Binds to:** `TestWatch_ContextCancelTerminates`

---

## RULE-HIDRAW-06 — ioctl numbers MUST match kernel uapi layout

Compile-time `unsafe.Sizeof` assertions in `init_test.go` MUST verify:

- `unsafe.Sizeof(hidrawDevinfo{}) == 8` (matches
  `struct hidraw_devinfo { __u32 bustype; __s16 vendor; __s16 product; }`)
- `HIDIOCGRAWINFO == 0x80084803` on amd64/arm64 (derived from
  `_IOR('H', 0x03, struct hidraw_devinfo)` with generic `_IOC` layout)
- `HIDIOCGRDESCSIZE == 0x80044801` on amd64/arm64

**Rationale:** silent ABI drift between architectures is the #1 pure-Go
syscall footgun. An ioctl number computed wrong passes the compiler,
fails at runtime with EINVAL, and is misdiagnosed as a kernel bug. The
compile-time assertion catches layout drift before the CI syscall tests
run.

**Binds to:** `TestIoctlNumbers_MatchKernelUAPI`
