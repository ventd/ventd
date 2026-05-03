# Diag bundle threat model 2024-2026

Captured 2026-05-02 from a research-agent pass. Twelve secret/PII categories
worth adding to the redactor blocklist or denylist beyond the existing
hostnames / MACs / IPs / usernames / SSH+TLS keys / shadow / sudoers coverage.

**Status: research only — no implementation PRs landing without review.**

## Categories

### 1. Tailscale auth keys & node state
- **Path:** `/var/lib/tailscale/tailscaled.state`, `/etc/default/tailscaled`
- **Regex:** `tskey-(auth|api|client|webhook|scim)-[A-Za-z0-9]{16,}-[A-Za-z0-9]{32,}`
- **Reversible:** No (high entropy).
- **Strategy:** Denylist file. Scrub journalctl too (1.86.4+ self-redacts; older leaks).

### 2. Cloudflare Tunnel credentials
- **Path:** `/etc/cloudflared/*.json`, `~/.cloudflared/*.json`
- **Regex (in-file):** `"TunnelSecret"\s*:\s*"[A-Za-z0-9+/=]{40,}"`
- **Strategy:** Denylist file. Also scrub `Environment=TUNNEL_TOKEN=…` in service files.

### 3. Kubernetes SA tokens & kubeconfig
- **Path:** `/var/run/secrets/kubernetes.io/serviceaccount/token`, `~/.kube/config`,
  `/etc/kubernetes/*.conf`, `/etc/rancher/{k3s,rke2}/*.yaml`
- **Regex:** JWT `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`
- **CVE:** CVE-2024-3177.
- **Strategy:** Denylist token files; regex JWTs anywhere else.

### 4. OAuth / GitHub PATs / API tokens in unit files
- **Path:** `/etc/systemd/system/*.service*`, recurse `EnvironmentFile=` targets.
- **Regex:**
  - GitHub: `gh[psoru]_[A-Za-z0-9]{36}` and `github_pat_[A-Za-z0-9_]{82}`
  - Slack: `xox[baprs]-[A-Za-z0-9-]{10,}`
  - AWS: `AKIA[0-9A-Z]{16}`
  - Generic: `(?i)(api[_-]?key|token|secret|passwd|password)\s*[:=]\s*["']?[A-Za-z0-9_\-./+=]{16,}`

### 5. WiFi PSKs (existing coverage — confirm)
- `/etc/NetworkManager/system-connections/*.nmconnection` (mode 0600)
- Legacy `/etc/wpa_supplicant/wpa_supplicant*.conf`
- Reversible (rainbow-tablable on SSID+PSK4WAY) — denylist entirely.

### 6. systemd credentials (new since v250)
- **Path:** `/etc/credstore/**`, `/etc/credstore.encrypted/**`,
  `/run/credentials/**`, `/var/lib/systemd/credential.secret` (host key —
  catastrophic if leaked).
- Also redact `LoadCredential=` / `LoadCredentialEncrypted=` /
  `SetCredentialEncrypted=` *values* in unit files.

### 7. WireGuard
- **Path:** `/etc/wireguard/*.conf`, `/etc/wireguard/*.key`, `*.privkey`
- **Regex:** `^\s*(PrivateKey|PresharedKey)\s*=\s*[A-Za-z0-9+/]{42,44}=?\s*$`
- Keep `PublicKey` for diag value.

### 8. LUKS / systemd-cryptenroll TPM state
- LUKS2 metadata in headers, not on rootfs — no plaintext key material.
- **However redact:** `/etc/crypttab` `keyfile` paths + referenced `*.key`
  blobs; `/var/lib/systemd/pcrlock.d/**`; `/run/systemd/pcrlock*`.

### 9. FIDO2 / smartcard
- `/etc/u2f_mappings`, `/etc/pam.d/*` referenced `authfile=…`,
  `/etc/pkcs11/**` — public credential handles only, not secrets, but PII.

### 10. cloud-init user-data / vendor-data
- **Path:** `/var/lib/cloud/instance/{user-data.txt,vendor-data.txt,cloud-config.txt}`,
  `/var/lib/cloud/instances/*/`, `/var/lib/cloud/seed/**`, NoCloud ISO mounts.
- Snyk/GitGuardian flagged 28M+ leaked secrets in 2025 with cloud-init
  userdata a recurring source.
- **Strategy:** Denylist these paths entirely. Rarely needed for fan-control
  diag.

### 11. PostgreSQL / Redis / MySQL config & connection strings
- **Path:** `/etc/postgresql/**/postgresql.conf`, `*.pgpass`, `~/.pgpass`,
  `/etc/redis/redis.conf`, `/etc/mysql/**`, `/etc/my.cnf*`
- **Regex:**
  - libpq URI: `postgres(?:ql)?://[^:\s]+:([^@\s]+)@`
  - keyword form: `(?i)(password|passwd|pwd)\s*[:=]\s*['"]?([^\s'"]+)`
  - `.pgpass`: `^[^:]*:[^:]*:[^:]*:[^:]*:(.+)$`
  - Redis: `^\s*(requirepass|masterauth)\s+(\S+)`

### 12. Container registry creds
- **Path:** `~/.docker/config.json`, `/root/.docker/config.json`,
  `~/.config/containers/auth.json`, `/run/containers/*/auth.json`,
  `/etc/containers/auth.json`
- **Regex (in-file):** `"auth"\s*:\s*"[A-Za-z0-9+/=]{20,}"` (base64 of
  `user:password` — trivially reversible).
- **Strategy:** Denylist. Even masked `auth` field length leaks password length.

## General hardening

- Always scrub `journalctl` exports for §1, §3, §4, §11 patterns post-collection.
- Apply regexes to `environ` of running processes if `/proc/*/environ` is
  snapshotted — common diag-bundle blind spot for §4.

## Sources

Tailscale data-at-rest blog · Cloudflare Tunnel permissions docs · CVE-2024-3177 ·
GitGuardian Kubernetes JWT detector · GitHub PAT regex reference (Microsoft Purview) ·
systemd CREDENTIALS spec · WireGuard config format · systemd-cryptenroll ArchWiki ·
Yubico pam-u2f · cloud-init secrets practices (OpenShift) ·
Snyk: 28M+ secrets leaked on GitHub in 2025 · containers-auth.json docs ·
CVE-2020-15157 containerd cred leak · NetworkManager keyfile format (RHEL 9).
