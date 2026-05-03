# Web UI accessibility + security audit (WCAG 2.2 / OWASP)

Captured 2026-05-02 from a research-agent pass against `/web/` + `/internal/web/`.

**Status: research only — no implementation PRs landing without review.**

## Top fixes (ordered by severity)

1. **Add CSP + security headers** in `server.go` middleware (S1, High)
2. **Login rate-limit / lockout** in `auth.go` (S2, High)
3. **Global `:focus-visible`** in `shell.css` / `tokens.css` (A2, High)
4. **Retire `--fg3` as text colour**; darken drifting-pill bg (A1, Medium)
5. **Tighten `prefers-reduced-motion`**: add `animation-delay:0s` to wildcard (A3, Medium)
6. **`SameSite=Lax` → `Strict`** on session cookie (S3, Medium)

---

## Accessibility (WCAG 2.2 AA)

### A1 — Colour contrast token pairs (dark theme)

| Pair | Ratio | Verdict |
|---|---|---|
| `--fg1 #e6edf3` on `--bg #0d1117` | ~15.4 : 1 | Pass |
| `--fg #c9d1d9` on `--bg` | ~12.3 : 1 | Pass |
| `--fg2 #8b949e` on `--bg` | ~5.5 : 1 | Pass |
| **`--fg3 #484f58` on `--bg`** | **~2.4 : 1** | **FAIL** (1.4.3) |
| `--teal #4fc3a1` text on `--bg` | ~7.9 : 1 | Pass |
| `--amber #e6a23c` on `--bg` | ~7.7 : 1 | Pass |
| `--red #f85149` on `--bg` | ~4.7 : 1 | Pass (just) |
| `--blue #58a6ff` on `--bg` | ~6.4 : 1 | Pass |
| **Drifting pill `#ee4d5a` on `#3a1f24`** | **~3.2 : 1** | **FAIL for body text** (1.4.3); passes large/3:1 non-text only |

**Fix:** Forbid `--fg3` for any text role; demote to border-only token.
Darken drifting-pill bg to `#2a1418` (raises ratio > 4.5 : 1).

### A2 — Focus-visible missing on dashboard + calibration

`shell.css` defines `:focus-visible` for `.nav-item`, `.btn`, `.icon-btn`.
**`dashboard.css` and `calibration.css` define ZERO `:focus-visible` rules** —
interactive tiles, recovery cards, calibration step buttons may rely on UA
defaults (often suppressed by `outline:none` resets).

Violates SC 2.4.7 Focus Visible (AA) and 2.4.11 Focus Not Obscured (AA, new
in 2.2).

**Fix:** Shared rule in `tokens.css` or `shell.css`:

```css
:where(button, a, [role=button], [tabindex]):focus-visible {
  outline: 2px solid var(--blue);
  outline-offset: 2px;
}
```

Audit each `.dash-tile`, `.cal-step`, `.recovery-card` for `tabindex` / role.

### A3 — `prefers-reduced-motion` incomplete

`shell.css` has the global wildcard rule (good baseline). `calibration.css`
has its own block listing 11 selectors. `dashboard.css` only gates the
confidence pill animations explicitly. The wildcard
`*{animation-duration:0.001ms!important}` in `shell.css` *should* catch
`dash-shimmer`, `dash-pulse`, `dash-fan-spin`, `dash-opp-pulse`,
`dash-conf-sweep`, `cal-grid-drift`, `cal-glow-a/b/c`, `cal-comet`,
`brand-spin` — **but only if `shell.css` loads before page CSS and the page
rules don't use `!important` themselves**. Verify cascade order.

**Fix:** Add to the global block:
`animation-delay: 0s !important; animation-iteration-count: 1 !important;`.
Explicitly null `dash-fan-spin` and `cal-fan-spin` (continuous rotation can
trigger vestibular issues even at 0.001ms duration if duration override fails).

### A4 — Screen-reader labels

Confirmed: demo-mode banner has `role="status"`. Likely missing —
fan-spin SVG icons (need `aria-hidden="true"`), the calibration "X of Y"
live region (needs `role="status" aria-live="polite"`), error toasts
(need `role="alert"`), icon-only `.icon-btn` instances (need `aria-label`).

### A5 — Form labels

Login / setup / curve-editor: enforce every `<input>` has either
`<label for>` or `aria-labelledby`; password fields have
`autocomplete="current-password"`; setup wizard uses `<fieldset><legend>`.

---

## Security (OWASP)

### S1 — CSP currently absent

Per RULE-UI-01 (no inline JS, no CDN), the strictest viable header — no nonce
machinery needed:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self';
  img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none';
  base-uri 'self'; form-action 'self'; frame-ancestors 'none'; upgrade-insecure-requests
```

Plus: `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`Permissions-Policy: interest-cohort=()`,
`Strict-Transport-Security: max-age=63072000; includeSubDomains` (only
when TLS active — code already gates this).

### S2 — Login brute-force

`auth.go` has bcrypt cost 12 (good) but no failed-attempt counter.

**Fix:** Per-account exponential backoff (sleep 2^n seconds after n failures,
cap 30 s) + soft lockout after 10 failures within 15 min. Don't lock by IP
alone (admin behind NAT).

### S3 — CSRF on POST endpoints

Server has an `originCheck` middleware wrapping mutations and `SameSite=Lax`
cookies. For `/api/setup/apply`, `/api/setup/reset`, `/api/system/reboot`,
`/api/v1/panic`: **upgrade `SameSite=Lax → Strict`** (single-user admin, no
inbound deep-links from email). Origin/Referer + Strict cookie is sufficient
per OWASP CSRF Cheat Sheet for same-origin-only apps.

### S4 — First-boot self-signed TLS

Recommended flow: ventd ships self-signed + a `/setup` wizard that
(a) accepts an uploaded PEM/key, (b) offers ACME via DNS-01 if user provides
API token, (c) leaves a persistent banner "Using self-signed cert — replace
in Settings → TLS." Don't pretend to be a real CA.

### S5 — Logout

`auth.go`'s `delete()` removes the server-side token in addition to clearing
the cookie — **correct per OWASP Session Management Cheat Sheet**. Verify
session-map deletion is mutex-guarded.

### S6 — CORS

`originCheck` is the right primitive — daemon should reject any request
whose `Origin` header doesn't match the host. Confirm: no
`Access-Control-Allow-Origin` header is ever emitted; preflights for
cross-origin POST should 403.

## Sources

WCAG 2.2 AA criteria · OWASP Authentication / Session Management / CSRF
Cheat Sheets · OWASP Secure Headers Project · MDN CSP docs.
