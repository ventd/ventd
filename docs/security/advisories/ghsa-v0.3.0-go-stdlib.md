# Advisory Draft: Go stdlib CVE set fixed in ventd v0.3.0

> **Status:** DRAFT — to be pasted into GitHub's advisory UI by a maintainer
> with repository Security Advisory permissions. ventd CI cannot file advisories
> directly; this document is the source of truth for the advisory body.
>
> **Maintainer action required:** Open
> https://github.com/ventd/ventd/security/advisories/new, set the fields below,
> and publish. Close this file in the same PR that publishes the advisory on
> GitHub, replacing the body with the resulting GHSA-XXXX-XXXX-XXXX URL.

---

## Summary

ventd versions prior to v0.3.0 were built with Go 1.25.0, which contains
17 reachable vulnerabilities in the Go standard library spanning
`crypto/tls`, `crypto/x509`, `net/http`, `net/url`, `encoding/pem`,
`encoding/asn1`, and `os`. All vulnerabilities are denial-of-service or
incorrect-behaviour class; none enable remote code execution against ventd
specifically.

The Go toolchain was bumped to 1.25.9 for v0.3.0. `govulncheck ./...` now
reports zero reachable vulnerabilities.

---

## Affected versions

- `< v0.3.0` (all builds against Go 1.25.0 through 1.25.8)

## Patched versions

- `>= v0.3.0` (built with Go 1.25.9)

---

## Vulnerability table

All 17 entries below were confirmed **reachable** by `govulncheck ./...`
run against the Go 1.25.0 module graph.  A finding is reachable when the
call graph from ventd source reaches the vulnerable symbol.

| # | GO ID | CVE ID | stdlib package | Fixed in Go | Severity | Summary |
|---|-------|--------|----------------|-------------|----------|---------|
| 1 | [GO-2025-4007](https://pkg.go.dev/vuln/GO-2025-4007) | CVE-2025-58187 | `crypto/x509` | 1.25.3 | **HIGH** | Quadratic complexity when checking name constraints |
| 2 | [GO-2025-4008](https://pkg.go.dev/vuln/GO-2025-4008) | CVE-2025-58189 | `crypto/tls` | 1.25.2 | MEDIUM | ALPN negotiation error contains attacker-controlled information |
| 3 | [GO-2025-4009](https://pkg.go.dev/vuln/GO-2025-4009) | CVE-2025-61723 | `encoding/pem` | 1.25.2 | **HIGH** | Quadratic complexity when parsing some invalid inputs |
| 4 | [GO-2025-4010](https://pkg.go.dev/vuln/GO-2025-4010) | CVE-2025-47912 | `net/url` | 1.25.2 | MEDIUM | Insufficient validation of bracketed IPv6 hostnames |
| 5 | [GO-2025-4011](https://pkg.go.dev/vuln/GO-2025-4011) | CVE-2025-58185 | `encoding/asn1` | 1.25.2 | **HIGH** | Parsing DER payload can cause memory exhaustion |
| 6 | [GO-2025-4012](https://pkg.go.dev/vuln/GO-2025-4012) | CVE-2025-58186 | `net/http` | 1.25.2 | **HIGH** | Lack of limit when parsing cookies can cause memory exhaustion |
| 7 | [GO-2025-4013](https://pkg.go.dev/vuln/GO-2025-4013) | CVE-2025-58188 | `crypto/x509` | 1.25.2 | **HIGH** | Panic when validating certificates with DSA public keys |
| 8 | [GO-2025-4155](https://pkg.go.dev/vuln/GO-2025-4155) | CVE-2025-61729 | `crypto/x509` | 1.25.5 | MEDIUM | Excessive resource consumption when printing host-cert validation error |
| 9 | [GO-2025-4175](https://pkg.go.dev/vuln/GO-2025-4175) | CVE-2025-61727 | `crypto/x509` | 1.25.5 | **HIGH** | Improper application of excluded DNS name constraints for wildcard names |
| 10 | [GO-2026-4337](https://pkg.go.dev/vuln/GO-2026-4337) | CVE-2025-68121 | `crypto/tls` | 1.25.7 | MEDIUM | Unexpected session resumption |
| 11 | [GO-2026-4340](https://pkg.go.dev/vuln/GO-2026-4340) | CVE-2025-61730 | `crypto/tls` | 1.25.6 | **HIGH** | Handshake messages may be processed at incorrect encryption level |
| 12 | [GO-2026-4341](https://pkg.go.dev/vuln/GO-2026-4341) | CVE-2025-61726 | `net/url` | 1.25.6 | **HIGH** | Memory exhaustion in query parameter parsing |
| 13 | [GO-2026-4601](https://pkg.go.dev/vuln/GO-2026-4601) | CVE-2026-25679 | `net/url` | 1.25.8 | MEDIUM | Incorrect parsing of IPv6 host literals |
| 14 | [GO-2026-4602](https://pkg.go.dev/vuln/GO-2026-4602) | CVE-2026-27139 | `os` | 1.25.8 | **HIGH** | FileInfo can escape from a Root in os |
| 15 | [GO-2026-4870](https://pkg.go.dev/vuln/GO-2026-4870) | CVE-2026-32283 | `crypto/tls` | 1.25.9 | **HIGH** | Unauthenticated TLS 1.3 KeyUpdate record — persistent connection retention and DoS |
| 16 | [GO-2026-4946](https://pkg.go.dev/vuln/GO-2026-4946) | CVE-2026-32281 | `crypto/x509` | 1.25.9 | MEDIUM | Inefficient policy validation |
| 17 | [GO-2026-4947](https://pkg.go.dev/vuln/GO-2026-4947) | CVE-2026-32280 | `crypto/x509` | 1.25.9 | MEDIUM | Unexpected work during chain building |

> Severity ratings use the CVSS 3.1 qualitative scale (LOW / MEDIUM / HIGH /
> CRITICAL) as published on pkg.go.dev.  DoS-class vulnerabilities that require
> attacker-controlled input reaching a network-accessible service are rated HIGH;
> incorrect-behaviour without an obvious exploitation path are rated MEDIUM.

---

## Workarounds

**Upgrade to ventd v0.3.0.** No configuration changes are required.

For users who cannot upgrade immediately:
- Restrict access to the ventd web UI (port 9999) to trusted networks only
  (firewall, Nginx/Caddy reverse proxy with auth).
- The `crypto/x509` and `crypto/tls` vulnerabilities are only reachable when
  TLS is enabled (`web.tls_cert` / `web.tls_key` set in config) or when the
  hwdb refresh feature is used (`--refresh-hwdb`).  Disabling both limits
  attack surface until upgrade is possible.

---

## Attack surface in ventd

ventd exposes one network endpoint: the web UI on `0.0.0.0:9999` (HTTPS
when TLS is configured).  The reachable call paths for each category:

- **`crypto/tls` / `crypto/x509`** — reachable via TLS handshake on the
  listening port (`internal/web/server.go:ListenAndServe`) and outbound
  hwdb refresh (`internal/hwdb/refresh.go`).
- **`net/http` / `net/url`** — reachable via all inbound HTTP requests.
- **`encoding/pem`** — reachable via self-signed cert generation on first
  boot (`internal/web/selfsigned.go:fingerprintCert`).
- **`encoding/asn1`** — reachable transitively through TLS handshake.
- **`os`** (GO-2026-4602) — reachable via sysfs directory enumeration in
  `internal/hwmon/enumerate.go` and `internal/config/resolve_hwmon.go`.

---

## References

- Go security release notes:
  - [Go 1.25.2](https://go.dev/doc/devel/release#go1.25.2)
  - [Go 1.25.3](https://go.dev/doc/devel/release#go1.25.3)
  - [Go 1.25.5](https://go.dev/doc/devel/release#go1.25.5)
  - [Go 1.25.6](https://go.dev/doc/devel/release#go1.25.6)
  - [Go 1.25.7](https://go.dev/doc/devel/release#go1.25.7)
  - [Go 1.25.8](https://go.dev/doc/devel/release#go1.25.8)
  - [Go 1.25.9](https://go.dev/doc/devel/release#go1.25.9)
- Go vulnerability database: https://pkg.go.dev/vuln/
- `govulncheck` tool: https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck
- ventd v0.3.0 release: pending tag

---

## GitHub Advisory UI field guide

When pasting this into the advisory form, set:

| Field | Value |
|-------|-------|
| Ecosystem | Go |
| Package name | `github.com/ventd/ventd` |
| Affected version range | `< v0.3.0` |
| Patched version | `v0.3.0` |
| Severity | High |
| CVE ID | (request assignment via GitHub form) |
| Description | paste the Summary + table above |

Cross-reference each upstream CVE ID in the "Related advisories" section.
