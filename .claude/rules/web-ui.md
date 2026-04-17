# Web UI Rules

- All UI is server-side rendered HTML/JS embedded in ui.go — no build step, no npm, no node
- Static assets served via Go embed directive
- API endpoints under /api/ return JSON
- Setup wizard at /api/setup/* — only active when no config exists or daemon is in setup mode
- Auth handled in auth.go — check authentication before adding new endpoints
- Listen address defaults to 0.0.0.0:9999 — accessible on the local network out of the box
- Authentication is required for all routes except /login, /logout, /api/ping — enforced in auth.go middleware
- First-boot: daemon prints a one-time setup token to stdout; browser auto-detects first-boot and shows token+password form
- For HTTPS: set web.tls_cert and web.tls_key in config, or front with Nginx/Caddy (recommended for Let's Encrypt)
- Keep JS minimal and vanilla — no frameworks, no transpilation
