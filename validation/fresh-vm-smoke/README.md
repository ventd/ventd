# Fresh-VM install smoke (Proxmox)

End-to-end verification that `scripts/install.sh` turns a pristine Linux VM into
a working ventd install. Each run:

1. Packs the current binary + `scripts/` + `deploy/` into a release-style
   `bundle.tar.gz` and serves it over a local HTTP server.
2. Clones a cloud-init template on a Proxmox host into a disposable VMID.
3. Injects a cloud-init user-data snippet that pulls `bundle.tar.gz`,
   extracts it, and runs the real `scripts/install.sh` the same way a release
   install would.
4. Waits for the service, collects assertion inputs into a small JSON,
   reads it back over the QEMU guest agent, and verifies it.
5. Destroys the disposable VM on every exit path (success, failure, Ctrl-C).

A single run covers one distro. `matrix.sh` sweeps the full set serially.

## Prerequisites

### On the Proxmox host

- SSH key trust from the dev host to `root@<pve>` (the harness drives
  everything remotely via `ssh`).
- QEMU guest agent enabled on templates (`--agent enabled=1`). The snippet
  also installs `qemu-guest-agent` in-guest on first boot as a fallback.
- `snippets` content enabled on the storage that hosts cloud-init user-data.
  On a default `local` dir storage:

  ```
  pvesm set local --content iso,vztmpl,backup,import,snippets
  mkdir -p /var/lib/vz/snippets
  ```

- One cloud-init template per distro you want to cover. The default VMID
  mapping is:

  | Distro         | Template VMID |
  |----------------|--------------:|
  | `ubuntu-24.04` | 9000          |
  | `debian-12`    | 9001          |
  | `fedora-40`    | 9002          |
  | `arch`         | 9003          |

  Bootstrap steps for Ubuntu 24.04 (others follow the same pattern — swap the
  cloud image path and the VMID):

  ```
  # One-time — cloud image already lives under /var/lib/vz/template/iso/
  qm create 9000 \
      --name ventd-tpl-ubuntu-2404 \
      --memory 2048 --cores 2 \
      --net0 virtio,bridge=vmbr0 \
      --scsihw virtio-scsi-single \
      --agent enabled=1 \
      --ostype l26 \
      --serial0 socket --vga serial0 \
      --boot order=scsi0
  qm set 9000 --scsi0 local-lvm:0,import-from=/var/lib/vz/template/iso/ubuntu-24.04-cloud.img,discard=on,ssd=1
  qm set 9000 --ide2 local-lvm:cloudinit
  qm set 9000 --ipconfig0 ip=dhcp
  qm disk resize 9000 scsi0 +8G
  qm template 9000
  ```

- VMID range 9010–9099 free (configurable via `--vmid-start` / `VMID_END`).
  Disposable VMs land in this range.

### On the dev host

- Go toolchain (matrix.sh builds the binary with `go build ./cmd/ventd` if
  you don't pass one).
- `python3` (drives the temporary HTTP server and JSON post-processing).
- `curl`, `ssh`, `scp`.
- Network path from the Proxmox VM bridge to an IP on the dev host — the
  harness autodetects `SSH_CLIENT` from the pve side, which is almost always
  correct when dev and pve are on the same LAN or tailnet.

## Running

Single distro:

```
./validation/fresh-vm-smoke/run.sh \
    --distro ubuntu-24.04 \
    --binary $(go env GOPATH)/bin/ventd        # or any locally-built binary
```

Whole matrix:

```
./validation/fresh-vm-smoke/matrix.sh          # builds binary, sweeps all
./validation/fresh-vm-smoke/matrix.sh ubuntu-24.04 debian-12    # subset
```

Override the Proxmox host:

```
PVE_HOST=root@other-pve ./validation/fresh-vm-smoke/run.sh --distro ubuntu-24.04 --binary ./ventd
```

Keep a VM alive for post-mortem on failure:

```
./validation/fresh-vm-smoke/run.sh --distro fedora-40 --binary ./ventd --keep-on-failure
# → the disposable VMID is left running; destroy it manually when done
```

## Assertions

The in-guest collector (`/usr/local/sbin/ventd-smoke-collect.sh`, shipped via
cloud-init) writes a single JSON document. `run.sh` checks:

| Assertion                   | Source                                                             |
|-----------------------------|--------------------------------------------------------------------|
| `install.sh` exited 0       | `install_rc` captured from the install runcmd step                 |
| Service is active           | `systemctl is-active ventd`                                        |
| Port :9999 is bound         | `ss -Hltn 'sport = :9999'`                                         |
| `/api/ping` returns 200     | HTTPS first (first-boot auto-cert), plaintext as fallback          |
| Setup token logged          | `journalctl -u ventd` contains `setup token`                       |
| `/etc/ventd` exists         | directory created by `install.sh`                                  |
| Daemon runs as `ventd`      | `ps -o user= -p $(pidof ventd)`                                    |

`config.yaml` is deliberately NOT asserted: the install script creates
`/etc/ventd/` but leaves config generation to the first-boot wizard in the web
UI. Asserting the file would make every fresh-install run a false negative.

## Expected wall-clock time

Roughly 3–5 minutes per distro on local-LVM backed storage:

| Phase                                      | Typical duration |
|--------------------------------------------|-----------------:|
| Clone + start                              |              ~5s |
| Cloud-init first boot (incl. apt update)   |           20–40s |
| Download bundle + run install.sh           |           10–30s |
| Daemon settle + assertion collection       |             5–15s |
| Destroy                                    |              ~5s |

A canonical Ubuntu 24.04 run on local-LVM typically comes in under 60s wall
clock.

`GUEST_TIMEOUT` (default 300s) bounds both the guest-agent wait and the
post-agent `DONE` marker wait. Long apt mirrors or slow package installs are
the usual cause of hitting it.

## Adding a new distro

1. Pick an unused template VMID (recommended: 9004, 9005, …).
2. Add the distro case to `template_for()` and `snippet_for()` in `run.sh`.
3. Bootstrap a template on the Proxmox host using that VMID and the distro's
   cloud image (see the Ubuntu bootstrap above as a model).
4. Copy one of the existing YAMLs under `snippets/` and adjust the package
   list for the target distro's package manager (`dnf`, `pacman`, etc.).
5. Run `./run.sh --distro <new-name> --binary ./ventd` and iterate.

## Safety notes

- The cleanup trap fires on every exit path — signal, error, normal exit. VMs
  in the 9010–9099 range should never accumulate. If they do, check the log
  for a trap that couldn't reach the Proxmox host (network drop).
- The script runs `qm destroy --purge`. This removes disks and cloud-init
  volumes for the disposable VMID only. Template VMIDs (9000–9003) are never
  touched.
- The HTTP server binds on `0.0.0.0:8765` by default. That's visible on the
  LAN while the run is in flight. Override `HTTP_BIND` / `HTTP_PORT` if that
  matters.
