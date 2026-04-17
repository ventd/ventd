# Cross-distro smoke harness

`scripts/cross-distro-smoke.sh` drives a Proxmox host through a full clone →
install → smoke → destroy cycle for every distro in the release matrix. It is
how a ventd release is certified against the distros listed in
[usability.md](../.claude/rules/usability.md): Ubuntu/Debian, Fedora,
Arch, openSUSE, Void, Alpine.

The harness runs on the dev host and talks to Proxmox over the REST API —
no SSH to the Proxmox node is required. It does SSH into each cloned VM
to run `scripts/install.sh` and check the service is up.

## What the harness asserts, per distro

1. `curl -fsSL .../install.sh | VENTD_VERSION=<tag> sudo -E bash` exits 0.
2. `systemctl is-active ventd` returns `active` within 60 s.
3. `curl http://<vm-ip>:9999/api/ping` returns HTTP 200 from the dev host.

On any failure, the last 100 lines of `journalctl -u ventd` are pulled
into the run log. The VM is then destroyed.

## Prerequisites

### Proxmox API token

Create under **Datacenter → Permissions → API Tokens**. The token ID
Proxmox prints (e.g. `root@pam!ventd-smoke`) and the secret UUID go into
`scripts/cross-distro-smoke.config.sh` as `PROXMOX_TOKEN_ID` and
`PROXMOX_TOKEN_SECRET`.

The token needs these privileges on path `/` with Propagate = 1:

- `VM.Allocate`
- `VM.Clone`
- `VM.PowerMgmt`
- `VM.Monitor`
- `VM.Config.Disk`
- `VM.Config.CPU`
- `VM.Config.Memory`
- `VM.Config.Options`
- `VM.Config.Network`
- `VM.Audit`
- `Datastore.AllocateSpace` (on the target storage)
- `Sys.Audit`

The simplest path is to disable **Privilege Separation** on the token so
it inherits a user with those roles. The harness never needs shell access
to the Proxmox node itself.

### Template prep, per distro

For each distro you want in the matrix, spin up a VM by hand, do the
steps below, and convert it to a template in the Proxmox UI.

1. **QEMU guest agent**. The harness uses it to learn the VM's IP after
   boot. Without it, `AGENT_IP_TIMEOUT` will always trip.

   ```
   # Ubuntu/Debian
   apt-get install -y qemu-guest-agent && systemctl enable --now qemu-guest-agent

   # Fedora/RHEL/openSUSE
   dnf install -y qemu-guest-agent   # or zypper install
   systemctl enable --now qemu-guest-agent

   # Arch
   pacman -S --noconfirm qemu-guest-agent
   systemctl enable --now qemu-guest-agent

   # Void
   xbps-install -Sy qemu-ga
   ln -s /etc/sv/qemu-ga /var/service

   # Alpine
   apk add qemu-guest-agent
   rc-update add qemu-guest-agent default
   rc-service qemu-guest-agent start
   ```

   In the Proxmox UI, also enable the agent on the VM itself:
   **Options → QEMU Guest Agent → Enabled**.

2. **Non-root SSH user with passwordless sudo**. The harness connects as
   `$SSH_USER` (default `ventd`) and runs the install script with `sudo -E`.

   ```
   useradd -m -G wheel ventd           # adjust group per distro
   install -d -m 0700 /home/ventd/.ssh
   cat > /home/ventd/.ssh/authorized_keys <<'KEY'
   ssh-ed25519 AAAA...   ventd-smoke
   KEY
   chown -R ventd:ventd /home/ventd/.ssh
   chmod 0600 /home/ventd/.ssh/authorized_keys

   echo 'ventd ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/ventd
   chmod 0440 /etc/sudoers.d/ventd
   ```

   The public key that goes into `authorized_keys` must be the pair of the
   private key you put in `SSH_KEY_PATH`. Generate a dedicated pair — don't
   reuse a personal key.

3. **Network + package cache**. Ensure the VM has DHCP (or a static IP
   that will survive a clone — DHCP is simpler), and that the distro's
   package index is warm so `install.sh`'s prerequisite install doesn't
   fail on first boot.

4. **Convert to template**. Right-click the VM in the Proxmox UI →
   **Convert to Template**. Note its VMID and put it in the
   `TEMPLATE_VMIDS` map:

   ```
   declare -A TEMPLATE_VMIDS=(
       [ubuntu-24-04]=9001
       [debian-12]=9002
       [fedora-40]=9003
       [arch]=9004
       [opensuse-tumbleweed]=9005
       [void-glibc]=9006
       [alpine-3-19]=9007
   )
   ```

   The slugs are fixed — they match the rows in
   [cross-distro-status.md](cross-distro-status.md). If you add a distro
   to the matrix, add a row to status too.

### Dev host

- `bash` 4+ (required for associative arrays — check with `bash --version`)
- `curl`, `jq`, `ssh`
- Network reachability from the dev host to the Proxmox API port (8006
  by default) and to each cloned VM on the VM bridge subnet.

## Running the harness

1. Copy the example config and edit it:

   ```
   cp scripts/cross-distro-smoke.config.sh.example \
      scripts/cross-distro-smoke.config.sh
   $EDITOR scripts/cross-distro-smoke.config.sh
   ```

   The real config is git-ignored. If the token ever lands in
   scrollback, a shared terminal, or any committed file, rotate it in
   the Proxmox UI immediately.

2. Run the full matrix:

   ```
   ./scripts/cross-distro-smoke.sh
   ```

3. Or run a single distro to iterate on template prep:

   ```
   ./scripts/cross-distro-smoke.sh ubuntu-24-04
   ```

A run takes roughly 2–5 minutes per distro with a full clone on local
SSD — longer on slower storage. Results land in:

- `docs/cross-distro-runs/<date>-<ventd-version>.md` — detailed log of
  this run, per-distro rows plus any journal tails pulled on failure.
- `docs/cross-distro-status.md` — cumulative matrix. The script appends a
  dated footer summarising the run; the main table stays human-owned.

## Interpreting the output

**Run file** (`docs/cross-distro-runs/<date>-<ventd-version>.md`) is
one per invocation. Each row is a distro with PASS/FAIL/SKIP, the clone's
VMID, the IP the guest agent reported, and a short reason. On any FAIL,
the journal tail is appended as a fenced code block under the table.
Safe to commit alongside the status update.

**Status file** (`docs/cross-distro-status.md`) has a hand-edited main
table — rows per distro, columns per architecture — plus an append-only
footer section per run. After reviewing the run file, promote cells in
the main table from `—` / `PENDING` to the actual result, then update
the "Last updated" line.

If the matrix has any red cells, file one issue per distro with the
journal tail — not a single mega-issue. Different distros fail for
different reasons (missing package, sysv init, SELinux, etc.) and one
issue each keeps the fix-forward cleanly scoped.

## Dry-run / CI mode

`PROXMOX_DRY_RUN=1` mocks every HTTP call and every SSH command, so the
harness exercises its config loading, task orchestration, and report
writing end-to-end without touching Proxmox or any VM:

```
PROXMOX_DRY_RUN=1 ./scripts/cross-distro-smoke.sh
```

This is what's exercised from CC terminals that don't have API access,
and what CI can run to catch regressions in the script itself.
