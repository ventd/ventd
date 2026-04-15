# UFW + Incus bridge firewall

How to run UFW on the dev host without breaking
`validation/fresh-vm-smoke.sh`. The rules themselves live in
[`ufw-incus.rules`](./ufw-incus.rules); this document is the operator
procedure around them.

## Background — why UFW defaults break the matrix

Ubuntu 24.04 ships UFW with `DEFAULT_FORWARD_POLICY="DROP"` and
`DEFAULT_INPUT_POLICY="DROP"`. Enabling UFW with just those defaults
breaks the smoke harness in two places:

1. **DHCP on `incusbr0` is blocked.** Containers launch, `dhclient`
   sends a `DHCPDISCOVER` to the host's `dnsmasq` on UDP/67, the
   default-deny-incoming policy drops it, and the container never
   gets an IP. Every harness target then fails in `launch_container`
   after `TIMEOUT_BOOT` expires.

2. **Guest-to-internet routing is blocked.** The containers have to
   reach `archive.ubuntu.com` (or `deb.debian.org` / `dl.fedoraproject.org`
   / …) to bootstrap `curl`. That path traverses the `FORWARD` chain,
   which the `deny-routed` default drops, so `apt-get update` stalls
   until the matrix aborts.

PR #50's summary noted this was why UFW ended up disabled during the
cross-distro gate. This file is the fix.

## The rule set

From the upstream Incus doc
<https://linuxcontainers.org/incus/docs/main/howto/network_bridge_firewalld/>,
adapted to Ubuntu 24.04's UFW 0.36:

1. **Base policies** — `deny incoming` (default), `allow outgoing`
   (default), **`allow routed`** (changed; FORWARD chain).
2. **Bridge opens** — `allow in on incusbr0`, `route allow in on
   incusbr0`, `route allow out on incusbr0`.
3. **DHCP v4 + v6** on UDP/67 and UDP/547. The upstream doc calls these
   out separately because `allow in on <br>` alone is not sufficient
   for broadcast DHCP on some UFW builds.
4. **DNS** on port 53 (tcp + udp). Guests use the host's dnsmasq.

See `ufw-incus.rules` for the exact `ufw` commands.

## Apply

```
sudo bash validation/ufw-incus.rules      # stage rules into /etc/ufw/*.rules
sudo ufw enable                           # activate the firewall
sudo ufw status verbose                   # sanity
```

Expected `ufw status verbose` tail after enable (rule order will vary):

```
Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), allow (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
Anywhere on incusbr0       ALLOW IN    Anywhere
67/udp on incusbr0         ALLOW IN    Anywhere
547/udp on incusbr0        ALLOW IN    Anywhere
53 on incusbr0             ALLOW IN    Anywhere
Anywhere                   ALLOW FWD   Anywhere on incusbr0
Anywhere on incusbr0       ALLOW FWD   Anywhere
```

## Test (before committing to UFW being on)

The user-facing gate for "it worked" is a full harness pass against at
least one distro with UFW active. With the rules staged but UFW still
inactive, the harness already passes (PR #70 baseline). The real test
is that enabling doesn't regress it.

```
# Apply rules and enable.
sudo bash validation/ufw-incus.rules
sudo ufw enable

# Run the canonical distro under UFW active.
validation/fresh-vm-smoke.sh ubuntu-24.04

# Expect: PASS 9/9, matching the pre-UFW baseline. The report under
#   validation/fresh-vm-smoke-ubuntu-24.04-<date>.md is the artefact.
```

If the harness fails with UFW active, disable and capture evidence for
iteration:

```
sudo ufw status verbose > /tmp/ufw-before-revert.log
sudo ufw disable
validation/fresh-vm-smoke.sh ubuntu-24.04    # confirm the harness passes with UFW off
```

Then iterate on `ufw-incus.rules` and re-test.

## Rollback

```
sudo ufw reset --force        # wipes all rules + disables UFW
```

This restores the host to "no firewall" — safe, but leaves the host
LAN-reachable on every listening port. For a half-rollback (keep the
staged rules but stop enforcing), use `sudo ufw disable`.

## Dry-run evidence

`ufw --dry-run` prints the nftables changes each rule would make
without actually applying them. Captured against this host with UFW
inactive:

```
$ sudo ufw --dry-run default allow routed
Default routed policy changed to 'allow'

$ sudo ufw --dry-run route allow in on incusbr0
*filter
:ufw-user-input - [0:0]
:ufw-user-output - [0:0]
:ufw-user-forward - [0:0]
…
-A ufw-user-forward -i incusbr0 -j ACCEPT
```

The other rules from `ufw-incus.rules` report `Skipping adding existing
rule` on this host because Incus's install hooks (or a previous attempt)
had already staged them in `/etc/ufw/user.rules`.

## Scope of this task

- [x] Rules scripted (`ufw-incus.rules`).
- [x] Procedure documented (this file).
- [x] Dry-run output captured as evidence.
- [ ] **Live test with UFW active** — deferred. The task that wrote
      this file was told explicitly not to enable UFW on the host. The
      operator enables + runs the matrix + confirms PASS on their next
      session, then merges the confirmation as a follow-up report
      under `validation/fresh-vm-smoke-ufw-active-<date>.md`.

## Caveat

Phoenix-MS-7D25 is the dev + Proxmox-controller host. Enabling UFW
here also affects SSH (port 22 is already staged in `ufw show added`,
but confirm before `ufw enable`) and Ollama (11434) / the dev HTTP
server (8080). Double-check your allow-list matches every service you
still want LAN-reachable before flipping the switch.
