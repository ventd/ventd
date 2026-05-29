# Security Policy

## Reporting a vulnerability

Do not open a public issue. Report vulnerabilities privately via [GitHub Security Advisories](https://github.com/ventd/ventd/security/advisories/new).

Include:

- A description of the vulnerability and its impact.
- Steps to reproduce, including hardware, distribution, kernel version, and `ventd` version.
- Any relevant logs or proof-of-concept code.
- Your assessment of severity.

You will receive an initial response within 72 hours. If the issue is confirmed, a fix will be developed privately, a CVE requested if appropriate, and a coordinated disclosure timeline agreed before public release.

## Scope

In scope:

- Remote code execution via the web UI, API, or setup wizard.
- Authentication bypass in the web UI: reaching an authenticated endpoint without a valid session, defeating the CSRF or Origin checks, or session-token prediction/fixation.
- Privilege escalation from an unauthenticated context to daemon privileges, **beyond** the documented first-boot window (see "First-boot trust model" below).
- Any condition where `ventd` writes values that damage hardware (exceeds safe PWM range, ignores pump minimum, fails to restore state on crash).
- Data exposure (session tokens, password hashes, calibration data accessed by unauthorised users), including secrets that leak into diagnostic bundles.
- Supply-chain issues in release artifacts, including a release whose signature or checksum does not verify against the published one.

Out of scope:

- Issues requiring local root access (the shipped systemd unit runs the daemon as `root`).
- Denial of service by flooding the local machine with requests — the web UI binds to the local network by default and is not designed for public internet exposure.
- Missing hardening recommendations (HSTS, CSP) when `ventd` is fronted by a reverse proxy, which is the recommended HTTPS deployment.

## First-boot trust model

`ventd` enrols its first admin password through a one-step wizard. The gap
between starting the daemon and an operator completing that wizard is a
**claim window**: until a password is set, whoever sets one first owns the
daemon (root-equivalent). The window is guarded as follows:

- **Loopback** enrolment (browsing from the host itself, or through an SSH
  tunnel) is **tokenless** — the request already proves on-box access.
- **Non-loopback** enrolment (any other machine on the network) must present
  a **one-time setup token** minted at first boot. The token is written to the
  daemon log (`journalctl -u ventd`) and to a root-only file
  (`/run/ventd/setup-token`) that the installer prints, and is sent as the
  `X-Ventd-Setup-Token` header or `setup_token` form field. It is retired the
  instant a password is set.

Treat first-boot setup like setting the password on any new appliance: do it
promptly, on a network you trust. Completing the wizard from localhost without
a token, or from the LAN *with* the minted token, is **expected behaviour**,
not a vulnerability. In scope: completing non-loopback enrolment **without** a
valid token, recovering the token without root, resetting an already-enrolled
daemon back into the unenrolled state remotely, or bypassing auth *after* a
password is set.

## Hardware safety

`ventd` controls physical fans. A bug that leaves a fan at PWM=0, bypasses the pump minimum floor, or fails to restore `pwm_enable` on exit is treated as a security issue, not a bug. Report it via the private channel above.

## Supported versions

Only the latest tagged release receives security fixes. Pre-1.0 releases are supported on a best-effort basis.
