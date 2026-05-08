# Web UI Rules

- All UI is server-side rendered HTML/JS embedded in ui.go — no build step, no npm, no node
- Static assets served via Go embed directive
- API endpoints under /api/ return JSON
- Setup wizard at /api/setup/* — only active when no config exists or daemon is in setup mode
- Auth handled in auth.go — check authentication before adding new endpoints
- Listen address defaults to 0.0.0.0:9999 — accessible on the local network out of the box
- Authentication is required for all routes except /login, /logout, /api/ping — enforced in auth.go middleware
- First-boot: no config + no auth.json → browser shows the password-set form, then logs in directly. Setup-token bootstrap was eliminated in v0.5.8.1 (#765, #794) when the daemon flipped to `User=root`.
- For HTTPS: set web.tls_cert and web.tls_key in config, or front with Nginx/Caddy (recommended for Let's Encrypt)
- Keep JS minimal and vanilla — no frameworks, no transpilation

## RULE-WEB-UPDATE-STAGE-PATH-OUTSIDE-PRIVATETMP: in-UI update stages install.sh outside the daemon's PrivateTmp namespace.

`writeInstallShBytes` MUST stage the install.sh it writes (whether
fetched from the GitHub release or unpacked from the embedded copy)
under a host-shared, non-namespaced path that the transient
`ventd-update.service` spawned via `systemd-run` can read in its own
namespace.

The default staging directory is `/run/ventd` — host-visible
(`PrivateTmp=yes` does not namespace `/run`), already in
`ventd.service`'s `ReadWritePaths`, ephemeral (cleared on reboot so
no orphan litter), mode 0700 (no world-readable script bytes).

Why not `/tmp`: ventd.service ships `PrivateTmp=yes`. The daemon's
view of `/tmp` is a per-unit kernel namespace; a script staged there
from the daemon does not exist at that path on the host. The
transient `ventd-update.service` spawned via `systemd-run` runs in
the host namespace; `bash /tmp/<staged>.sh` returns exit 127 / ENOENT
and the unit fails. The API caller sees a successful 202 because
`realUpdateRun`'s `cmd.Run()` observed a successful systemd-run queue,
not the unit's runtime exit. Diagnosed end-to-end on Phoenix's MSI
Z690-A desktop on 2026-05-08; latent since the systemd-run pattern
landed.

The package-level `installStagingDir` seam holds the staging path.
Production sets it to `/run/ventd`; tests override it to a
`t.TempDir()` so the assertions don't need root. An empty seam value
means "use the default tmp dir" — no shipping code uses this today,
but the seam preserves the legacy behaviour as an escape hatch.

The fallback branch lands when `installStagingDir` is non-empty but
the directory cannot be created or proven writable (dev-tree
invocation, sandbox-hardened env that doesn't grant `/run/ventd`,
non-systemd hosts). In that case the function falls through to
`os.CreateTemp("", ...)` so existing dev workflows + non-systemd
hosts keep working — but on production systemd hosts the staging
dir is always reachable and the fallback never fires.

The default-being-/run/ventd assertion is pinned independently of
the seam-override tests so a regression that defaults the seam back
to `""` (Go's `os.TempDir()` resolution) or to `"/tmp"` reintroduces
the silent-fail bug — even if the seam-override tests still pass.

Bound: internal/web/update_staging_test.go:staging_dir_default_is_run_ventd
Bound: internal/web/update_staging_test.go:happy_path_stages_under_configured_dir
Bound: internal/web/update_staging_test.go:falls_back_to_default_tmp_when_staging_dir_unwritable
Bound: internal/web/update_staging_test.go:empty_staging_dir_seam_uses_default_tmp
