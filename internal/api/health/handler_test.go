package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stick/internal/api/health"
	"stick/internal/web/httpx"
)

type healthDB struct {
	err      error
	deadline time.Time
}

func (d *healthDB) PingContext(ctx context.Context) error {
	d.deadline, _ = ctx.Deadline()
	return d.err
}

func TestHealthRoutes(t *testing.T) {
	for _, test := range []struct {
		name       string
		path       string
		readiness  *healthDB
		wantStatus int
		wantBody   string
	}{
		{name: "live despite dependency failure", path: "/healthz", readiness: &healthDB{err: errors.New("down")}, wantStatus: http.StatusOK, wantBody: "ok\n"},
		{name: "ready", path: "/readyz", readiness: &healthDB{}, wantStatus: http.StatusOK, wantBody: "ready\n"},
		{name: "dependency unavailable", path: "/readyz", readiness: &healthDB{err: errors.New("down")}, wantStatus: http.StatusServiceUnavailable, wantBody: "not ready\n"},
		{name: "missing dependency", path: "/readyz", wantStatus: http.StatusServiceUnavailable, wantBody: "not ready\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := httpx.NewRouter("/stick")
			var readiness health.ReadinessChecker
			if test.readiness != nil {
				readiness = test.readiness
			}
			health.New(readiness).Register(router)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stick"+test.path, nil))

			if recorder.Code != test.wantStatus || recorder.Body.String() != test.wantBody {
				t.Fatalf("response = %d %q, want %d %q", recorder.Code, recorder.Body.String(), test.wantStatus, test.wantBody)
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			if test.path == "/readyz" && test.readiness != nil {
				remaining := time.Until(test.readiness.deadline)
				if remaining <= 0 || remaining > health.ReadinessTimeout {
					t.Errorf("readiness deadline remaining = %s, want (0, %s]", remaining, health.ReadinessTimeout)
				}
			}
		})
	}
}
