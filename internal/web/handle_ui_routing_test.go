package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleUI_MonitorOnlyRoutesToHealth pins the #784 fix:
// when the wizard has applied AND no controllable channels were
// configured (monitor-only outcome — laptop/minipc/BMC-managed
// server), root navigation lands on /health instead of /dashboard.
//
// The dashboard's hero spark / fan tiles / smart pill are mostly
// empty for these hosts; /health surfaces every sensor + fan
// reading the daemon DOES enumerate, which is the meaningful view.
func TestHandleUI_MonitorOnlyRoutesToHealth(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// Synthesise the post-first-boot state: an admin password hash
	// is set so handleUI doesn't redirect to /setup before reaching
	// the monitor-only branch.
	hash := "$2a$10$test.placeholder.bcrypt.hash.is.not.real.but.lengthy.enough"
	srv.liveHash.Store(&hash)

	// Synthesise the post-wizard monitor-only state: setup-applied
	// marker present, zero entries in cfg.Controls.
	if srv.setup == nil {
		t.Fatal("harness has no setup manager")
	}
	srv.setup.MarkApplied()
	live := srv.cfg.Load()
	if len(live.Controls) != 0 {
		t.Fatalf("harness config seeded with %d controls; want 0 for monitor-only branch", len(live.Controls))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.handleUI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `url=/health"`) {
		t.Errorf("body did not redirect to /health; got: %q", body)
	}
	if strings.Contains(body, `url=/dashboard"`) {
		t.Errorf("body redirected to /dashboard; expected /health for monitor-only")
	}
}
