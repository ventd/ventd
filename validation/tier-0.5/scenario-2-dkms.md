# Scenario 2 — Missing DKMS

Host: `phoenix@192.168.7.208` (VM 207, Ubuntu 24.04 LTS).
Continuation of the same daemon session as Scenario 1 (headers have been
restored; DKMS is still absent — the clean Ubuntu image ships without it).

## Procedure

1. Confirm dkms absent: `which dkms` → empty; preflight `DKMS_MISSING`.
2. `POST /api/hwdiag/install-dkms` with Scenario 1 session cookie.
3. Verify response, verify `dkms` on PATH, verify preflight clears to `OK`.

## Evidence

### Baseline preflight (post Scenario 1, pre install-dkms)

```json
{
  "detail": "DKMS is not installed. Without it the synthetic module will need to be rebuilt manually after every kernel update.",
  "reason": 2,
  "reason_string": "DKMS_MISSING"
}
```

### Endpoint response

```json
{"kind":"install_log","success":true,"log":["Installing dkms via apt-get...","DKMS installed successfully."]}
```

### Post-install target state

```
$ which dkms
/usr/sbin/dkms
$ /tmp/preflight-check
{
  "detail": "",
  "reason": 0,
  "reason_string": "OK"
}
```

## Outcome

✓ `DKMS_MISSING` classified correctly. Endpoint installs dkms via apt,
returns success, and `hwdiag.Store.Remove(IDOOTDKMSMissing)` fires on
success (see `internal/web/server.go:705` and the dedicated handler at
`internal/web/server.go:719`). Preflight returns `OK` — full chain cleared.
