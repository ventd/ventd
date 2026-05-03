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
