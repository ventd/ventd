// Package webstatic holds the new design-system static assets for the ventd
// web UI. Files are embedded at build time; the server registers routes that
// serve them. sidebar.html and canon.md are test fixtures — they live on disk
// but are intentionally excluded from the embed so HTTP requests return 404.
package webstatic

import "embed"

//go:embed index.html index.css shared/tokens.css shared/shell.css shared/brand.css shared/brand.js shared/ambient.css setup.html setup.css setup.js calibration.html calibration.css calibration.js dashboard.html dashboard.css dashboard.js devices.html devices.css devices.js hardware.html hardware.css hardware.js curve-editor.html curve-editor.css curve-editor.js schedule.html schedule.css schedule.js sensors.html sensors.css sensors.js settings.html settings.css settings.js login.html login.js
var FS embed.FS
