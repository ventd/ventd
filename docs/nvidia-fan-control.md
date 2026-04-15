# NVIDIA GPU fan control

`ventd` reads NVIDIA GPU temperatures and fan RPMs using NVML (the NVIDIA
Management Library), which it loads at runtime via `dlopen`. No CGO,
no build-time coupling to the driver.

**Temperature reads work out of the box on any system with a working
NVIDIA driver.** If your GPU appears in `nvidia-smi`, `ventd` can read
its temperature and fan speed.

**Writing fan speed requires additional privileges that the install
script does not configure by default.** If the setup wizard logs:

```
nvml: set fan 0 speed device 0: Insufficient Permissions
```

the driver is refusing the fan-write even though the read succeeded. GPU
fan *control* will be non-functional until one of the options below is
applied. Hwmon (CPU, chassis, AIO) fans are unaffected and will calibrate
and run normally — this only disables GPU fan control.

## Why this happens

`nvmlDeviceSetFanSpeed_v2` is gated by the NVIDIA driver in a way that
differs from every other NVML call `ventd` makes. The driver requires
**one of** the following:

1. The caller holds `CAP_SYS_ADMIN`.
2. The NVIDIA X server is running with the `Coolbits` option set (bit 2
   for fan control, value `4`). Requires an active X session on the
   same user.
3. A custom udev rule places the ventd service user in a group
   NVML treats as privileged for fan control.

The `ventd.service` unit ships with `ProtectSystem=strict`,
`ProtectKernelTunables=yes`, `ProtectKernelModules=yes`,
`ProtectProc=invisible`, and no inherited capabilities. This is the
correct posture for a root-adjacent daemon that writes to sysfs and
does not need any of the privileges NVML's gate checks for. None of
the three paths above are auto-configured by the install script.

## Option A — udev rule (recommended)

Creates a dedicated `ventd` group, adds the service user to it, and
reassigns group ownership of the NVIDIA control nodes to that group.
The NVIDIA device nodes are world-readable/writable (`0666`) by
default, so this is not about filesystem permissions — it is about
NVML's internal gate for `SetFanSpeed_v2`, which checks the caller's
supplementary groups against the owning group of `/dev/nvidiactl`.
Setting `MODE="0660"` additionally tightens the posture (drops the
world-writable bit), which is why the rule is preferred over the
default state. No capability elevation, no X server, no cool-bits
flag.

```
# /etc/udev/rules.d/71-ventd-nvidia.rules
KERNEL=="nvidiactl", GROUP="ventd", MODE="0660"
KERNEL=="nvidia[0-9]*", GROUP="ventd", MODE="0660"
```

Apply:

```
sudo groupadd -f ventd
sudo usermod -aG ventd ventd   # add the ventd service user to the group
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=nvidia
sudo systemctl restart ventd
```

Then re-run the setup wizard from the web UI. Calibration should
complete for the GPU fan.

## Option B — cool-bits flag (X11 only)

Only viable on systems running an X server under the ventd service
user's session. Most server and headless installs are not eligible.

```
sudo nvidia-xconfig --cool-bits=4
# restart X (log out / log in)
```

## Option C — capability elevation (last resort)

Grants the daemon `CAP_SYS_ADMIN` at start. This undoes most of the
hardening rationale for the other unit options and is not recommended.
Kept here only as a reference for operators who explicitly need it and
are aware of the trade-off.

Override file at `/etc/systemd/system/ventd.service.d/nvidia-cap.conf`:

```
[Service]
AmbientCapabilities=CAP_SYS_ADMIN
CapabilityBoundingSet=CAP_SYS_ADMIN
```

Then:

```
sudo systemctl daemon-reload
sudo systemctl restart ventd
```

## How to tell which path your system is on

```
# Temperature read path — should always succeed if driver is healthy.
nvidia-smi --query-gpu=temperature.gpu --format=csv,noheader

# Fan-write capability — directly exercises the gate ventd hits.
# If this prints "Setting fan speed to 50% ... Successfully set fan speed"
# the daemon will also succeed. If it prints "Insufficient Permissions",
# one of the options above is needed.
nvidia-smi -i 0 -pm 1
nvidia-settings -a "[gpu:0]/GPUFanControlState=1" -a "[fan:0]/GPUTargetFanSpeed=50"
```

## v0.2.0 status

This is a documented known issue in the v0.2.0 release. The install
script does not auto-apply any of the options above. Future releases
may detect NVIDIA driver presence and offer to install the Option A
udev rule as part of the install flow.

GPU temperature reading works regardless; only fan control is gated.
Pure-hwmon rigs (no NVIDIA, or NVIDIA without user-requested GPU fan
control) are unaffected.
