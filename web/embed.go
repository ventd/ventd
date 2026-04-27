// Package webstatic holds the new design-system static assets for the ventd
// web UI. Files are embedded at build time; the server registers routes that
// serve them. sidebar.html and canon.md are test fixtures — they live on disk
// but are intentionally excluded from the embed so HTTP requests return 404.
package webstatic

import "embed"

//go:embed index.html index.css shared/tokens.css shared/shell.css shared/brand.css shared/brand.js
var FS embed.FS
