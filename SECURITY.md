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
- Authentication bypass in the web UI or setup token flow.
- Privilege escalation from an unauthenticated context to daemon privileges.
- Any condition where `ventd` writes values that damage hardware (exceeds safe PWM range, ignores pump minimum, fails to restore state on crash).
- Data exposure (session tokens, password hashes, calibration data accessed by unauthorised users).
- Supply-chain issues in release artifacts.

Out of scope:

- Issues requiring local root access (the daemon already runs as root).
- Denial of service by flooding the local machine with requests — the web UI binds to the local network by default and is not designed for public internet exposure.
- Missing hardening recommendations (HSTS, CSP) when `ventd` is fronted by a reverse proxy, which is the recommended HTTPS deployment.

## Hardware safety

`ventd` controls physical fans. A bug that leaves a fan at PWM=0, bypasses the pump minimum floor, or fails to restore `pwm_enable` on exit is treated as a security issue, not a bug. Report it via the private channel above.

## Supported versions

Only the latest tagged release receives security fixes. Pre-1.0 releases are supported on a best-effort basis.
