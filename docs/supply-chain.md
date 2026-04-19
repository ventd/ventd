# Supply-Chain Security

This document covers ventd's supply-chain security posture: dependency
management, CI hygiene, SBOM, signing, and the security advisory policy.

---

## Go toolchain pinning

`go.mod` carries an explicit `go <version>` directive that pins the minimum
Go toolchain.  The version is bumped to the latest Go patch release before
every tagged release.  `govulncheck ./...` must report zero reachable
vulnerabilities before a tag is cut.

The CI `build-and-test` matrix runs `govulncheck ./...` on every push and
pull request so regressions are caught before merge.

---

## Dependency hygiene

- All direct and indirect dependencies are recorded in `go.sum` with
  cryptographic hashes verified by the Go toolchain on every build.
- `go mod tidy` is enforced in CI; a dirty module graph blocks merge.
- External dependencies are kept minimal: the runtime binary links only
  `golang.org/x/sys`, `golang.org/x/crypto`, `gopkg.in/yaml.v3`,
  `github.com/ebitengine/purego` (NVML dlopen), and `github.com/sstallion/go-hid`
  (USB HID).  All others are test-only or tooling.
- `govulncheck` is run in source mode (symbol-level reachability) so only
  vulnerabilities in actually-called code paths are flagged.

---

## CI hygiene (SUPPLY-CI-HYGIENE-01)

All GitHub Actions workflows use:

- **SHA-pinned actions** — every `uses:` line carries the full commit SHA, not
  a floating tag.  A supply-chain compromise of an action cannot silently
  inject code.
- **Digest-pinned container images** — CI containers are referenced by
  `image@sha256:...` digest.  Tag mutation cannot change the executed image.
- **Hash-verified Go toolchain downloads** — the Alpine CI leg downloads the
  Go tarball and verifies its SHA-256 before extracting.

Renovate (or manual review) bumps SHA pins when upstream actions release
security patches.

---

## SBOM and signing (Phase 10 targets)

The following controls are **stubs** until Phase 10 land:

| Control | Status | Target |
|---------|--------|--------|
| SBOM generation (`cyclonedx-go` / `syft`) | planned | Phase 10 |
| Release binary signing (`cosign`) | planned | Phase 10 |
| Reproducible builds (build stamp) | planned | Phase 10 |
| SLSA provenance attestation | planned | Phase 10 |

The `drew-audit.yml` post-tag workflow has placeholder steps for all four;
they will become real checks once Phase 10 delivers the tooling.

---

## Security advisories

**Policy:** ventd issues one consolidated GitHub Security Advisory (GHSA)
per security release.  Each advisory cross-references all upstream CVE IDs
and Go vulnerability database IDs (GO-XXXX-YYYY) fixed in that release.

**Severity trigger policy:**

| Reachable severity | Action |
|--------------------|--------|
| CRITICAL | Patch release within **48 hours** of upstream disclosure |
| HIGH | Patch release within **7 days** of upstream disclosure |
| MEDIUM | Included in next planned release; patch release if no release is imminent within 30 days |
| LOW | Included in next planned release |

"Reachable" means `govulncheck` finds a call path from ventd source to the
vulnerable symbol.  An unreachable vulnerability in a dependency does not
trigger the above timelines but is tracked in the dependency bump queue.

**Filing process:**

1. A maintainer with repository Security Advisory permissions opens a draft
   advisory at `https://github.com/ventd/ventd/security/advisories/new`.
2. The advisory body is sourced from
   `docs/security/advisories/ghsa-<version>-<label>.md` in the release
   branch.
3. After publishing, the advisory file is updated with the resulting
   GHSA-XXXX-XXXX-XXXX URL and the draft marker is removed.

Advisory source files live in `docs/security/advisories/`.

### Published advisories

| Release | Advisory source | GHSA | CVEs |
|---------|----------------|------|------|
| v0.3.0 | [ghsa-v0.3.0-go-stdlib.md](security/advisories/ghsa-v0.3.0-go-stdlib.md) | pending publication | 17 Go stdlib CVEs (Go 1.25.0 → 1.25.9) |

---

## Vulnerability disclosure

To report a security vulnerability in ventd itself (not a dependency), use
GitHub's private vulnerability reporting:
`https://github.com/ventd/ventd/security/advisories/new`

Do not open a public issue for unpatched vulnerabilities.
