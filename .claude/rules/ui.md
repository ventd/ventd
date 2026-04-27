# Web UI Invariants (spec-12)

Rules are accumulated across spec-12 PRs. PR 1 defines RULE-UI-01..04 (global).
PRs 2-8 will extend this file with RULE-UI-DEVICES-*, RULE-UI-DASHBOARD-*, etc.

## RULE-UI-01: No external CDN dependencies. All CSS, JS, fonts, and images are served from ventd's own filesystem.

Static analysis walks `web/**/*.{html,css,js}` and greps for `http://` or
`https://` URL literals outside HTML comments and the DOCTYPE declaration. The
allowlist is: `//www.w3.org/` in xmlns attributes. A reference to an external
CDN (unpkg, cdnjs, fonts.googleapis.com, etc.) would fail under air-gapped
installs and violates the zero-external-dependency invariant that lets ventd
run offline.

Bound: internal/web/ui_tokens_test.go:TestUI_NoExternalCDN

## RULE-UI-02: Token-only color references. Page-specific CSS must not contain literal hex, rgb(), hsl(), or rgba() color values — every color must come from a var(--*) reference defined in tokens.css.

Static analysis walks `web/**/*.css` excluding ALL files under `web/shared/`
(tokens.css, shell.css, and brand.css are the design system and may use rgba()
for opacity variants that CSS variables cannot express without color-mix()), and
greps for hex color literals (`#[0-9a-fA-F]{3,8}\b`), `rgb(`, `hsl(`, `hsla(`,
and `rgba(` function calls on lines that do not also contain `var(`. Named CSS
color keywords are not linted (too many false positives — "red" appears in class
names). A hard-coded color outside tokens.css breaks the single-source-of-truth
invariant and creates design drift that RULE-UI-03 sidebar checking cannot catch.

Bound: internal/web/ui_tokens_test.go:TestUI_TokenOnlyColors

## RULE-UI-03: Sidebar markup matches web/shared/sidebar.html byte-for-byte (whitespace-normalised) across all pages that include a sidebar.

The canonical sidebar is defined once in `web/shared/sidebar.html`; each page
embeds a copy in its initial HTML (no SSI, no JS injection). The test reads the
canonical fixture, then for each `web/*.html` page extracts the
`<aside class="sidebar">…</aside>` block, normalises whitespace (collapse runs
to single space, trim), and asserts equality. Pages without a sidebar element
(e.g. `index.html`) are skipped. Sidebar drift between pages causes inconsistent
nav state and breaks RULE-UI-03 on CI before the difference reaches users.

Bound: internal/web/ui_tokens_test.go:TestUI_SidebarConsistency

## RULE-UI-04: Canonical data values from web/shared/canon.md are the source of truth for fixture data used in tests.

`canon.md` records the reconciled design-canonical values (host, fingerprint,
version, fan/chip/sensor counts, live readings). Any test fixture that stubs
these values must match canon.md. Runtime API responses are unconstrained — the
rule covers test fixtures only, not production data. Currently (PR 1) the test
verifies that `canon.md` exists and is non-empty; fixture-binding to specific
test data lands in PR 2 when the first non-stub API fixtures arrive.

Bound: internal/web/ui_tokens_test.go:TestUI_CanonFixtureSync
