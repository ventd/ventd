---
name: deploy-and-verify
description: |
  Use when deploying a freshly-built ventd binary to a dev VM (Proxmox host,
  MiniPC, etc.) and verifying it actually started. Codifies build → scp →
  md5 sanity → systemctl restart → tail journal until READY/fail. Do NOT
  use for: production deploys (Phoenix-only); cross-distro smoke (use
  scripts/cross-distro-smoke.sh); release tagging.
disable-model-invocation: true
argument-hint: <remote-host>[:install-path]
allowed-tools: Bash(make build) Bash(go build *) Bash(scp *) Bash(ssh *) Bash(md5sum *) Read
---

# deploy-and-verify

User-invoked. End-to-end deploy of a snapshot ventd binary to a dev VM with
mechanical verification at every step. Catches the install-path / md5 /
service-restart class of bugs before they show up as "ventd is dead and I
don't know why."

## When to use

- Iterating on a feature that needs HIL verification on a specific VM.
- Pushing a fix to the MiniPC or Proxmox dev host.

## When NOT to use

- **Production deploys are Phoenix-only.** Per collaboration.md.
- **Release tagging** is a separate workflow (ventd-release-validate).
- **Cross-distro smoke** uses `scripts/cross-distro-smoke.sh`.

## Inputs

- `remote-host` (required): SSH alias or `user@host`. Examples: `proxmox`,
  `minipc`, `phoenix@192.168.7.222`.
- `install-path` (optional, default `/usr/local/sbin/ventd`): where the
  binary lives on the remote. **Watch out:** the systemd unit's `ExecStart`
  must match. The `sbin` vs `bin` confusion is the #1 footgun.

## Procedure

1. **Build snapshot binary locally.**
   ```
   make build
   ```
   Verify `dist/ventd_linux_amd64_v1/ventd` (or arch-equivalent) exists.

2. **Read the remote unit's ExecStart to confirm install path.**
   ```
   ssh $REMOTE systemctl cat ventd | grep ^ExecStart
   ```
   The path printed here is the truth. Use it for step 3, regardless of
   what the input arg said.

3. **scp + md5 sanity check.**
   ```
   scp dist/ventd_linux_amd64_v1/ventd $REMOTE:/tmp/ventd-staging
   local_md5=$(md5sum dist/ventd_linux_amd64_v1/ventd | cut -d' ' -f1)
   remote_md5=$(ssh $REMOTE md5sum /tmp/ventd-staging | cut -d' ' -f1)
   ```
   Abort if they differ.

4. **Stop service, install, restart.**
   ```
   ssh $REMOTE 'sudo systemctl stop ventd \
     && sudo install -m 0755 /tmp/ventd-staging $INSTALL_PATH \
     && sudo systemctl start ventd'
   ```
   `install` (not `mv`) preserves SELinux contexts and explicit mode.

5. **Tail journal until READY=1 or fail.**
   ```
   timeout 30 ssh $REMOTE journalctl -u ventd -f --no-pager \
     | grep -m1 -E 'READY=1|Failed to start|FATAL|panic:'
   ```
   The `grep -m1` exits as soon as a match is seen. If 30s elapses with no
   match, the service is hung — surface this and do not declare success.

6. **Confirm pid and uptime.**
   ```
   ssh $REMOTE systemctl show ventd --property=MainPID,ActiveState,SubState,ActiveEnterTimestamp
   ```

## Failure modes this catches

- **Wrong install path** (sbin vs bin): step 2 reads the live unit's
  `ExecStart`, never trusts the operator's memory.
- **scp truncation**: step 3 md5-compares before installing.
- **Stale binary still running**: step 4 stops first, installs, then
  starts — no implicit reload-then-restart race.
- **Service didn't actually start**: step 5 waits for explicit
  READY=1 or failure marker, with a hard 30s ceiling.
- **Crash-loop on start**: step 6 surfaces the SubState (`failed`,
  `auto-restart`, `running`) so a healthy `start` exit code doesn't mask
  a daemon that died 1s later.

## Constraints

- Never deploy to a host whose hostname matches `prod-*` or
  `*.production.*` — that's Phoenix-only.
- Never `--no-verify` past the local pre-commit hook before calling this.
- Never `kill -9` the daemon — `systemctl stop` only.
