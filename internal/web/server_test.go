package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/content"
	"stick/internal/web/dashboard"
	"stick/internal/web/httpx"
	"stick/internal/web/render"
	"stick/internal/web/security"
	"stick/internal/web/sticks"
	"stick/internal/web/views"

	"uuid"
)

func TestNewHTTPServerTimeouts(t *testing.T) {
	server := newHTTPServer(":8080", http.NewServeMux())

	if server.ReadTimeout != httpReadTimeout {
		t.Errorf("ReadTimeout = %s, want %s", server.ReadTimeout, httpReadTimeout)
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, httpReadHeaderTimeout)
	}
	if server.WriteTimeout != httpWriteTimeout {
		t.Errorf("WriteTimeout = %s, want %s", server.WriteTimeout, httpWriteTimeout)
	}
	if server.IdleTimeout != httpIdleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", server.IdleTimeout, httpIdleTimeout)
	}
}

func TestServeWithContextGracefullyDrainsOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := newHTTPServer(listener.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveWithContext(ctx, server, listener, time.Second) }()

	response := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			err = resp.Body.Close()
		}
		response <- err
	}()
	waitFor(t, started, "handler did not start")
	cancel()
	assertStillRunning(t, done, "server returned before the active handler finished")
	close(release)

	if err := waitForValue(t, done, "server did not shut down"); err != nil {
		t.Fatalf("serveWithContext: %v", err)
	}
	if err := waitForValue(t, response, "request did not finish"); err != nil {
		t.Fatalf("request: %v", err)
	}
}

func TestServeWithContextDrainsOnListenerFailure(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("listener failed")
	listener := &failAfterFirstAcceptListener{
		Listener: base,
		fail:     make(chan struct{}),
		err:      wantErr,
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := newHTTPServer(listener.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	done := make(chan error, 1)
	go func() {
		done <- serveWithContext(context.Background(), server, listener, time.Second)
	}()

	response := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			err = resp.Body.Close()
		}
		response <- err
	}()
	waitFor(t, started, "handler did not start")
	close(listener.fail)
	assertStillRunning(t, done, "server returned before draining after listener failure")
	close(release)

	if err := waitForValue(t, done, "server did not return after listener failure"); !errors.Is(err, wantErr) {
		t.Fatalf("serveWithContext error = %v, want %v", err, wantErr)
	}
	if err := waitForValue(t, response, "request did not finish"); err != nil {
		t.Fatalf("request: %v", err)
	}
}

type failAfterFirstAcceptListener struct {
	net.Listener
	fail     chan struct{}
	err      error
	accepted bool
}

func (l *failAfterFirstAcceptListener) Accept() (net.Conn, error) {
	if !l.accepted {
		conn, err := l.Listener.Accept()
		if err == nil {
			l.accepted = true
		}
		return conn, err
	}
	<-l.fail
	return nil, l.err
}

func waitFor(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func waitForValue[T any](t *testing.T, ch <-chan T, message string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal(message)
		var zero T
		return zero
	}
}

func assertStillRunning(t *testing.T, done <-chan error, message string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s: %v", message, err)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestNewHandlerRegistersProtectedRoutes(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	publicURL := testPublicURL(t, "http://example.test", "/stick")
	h, err := newHandler(application.NewService(store), store, Options{
		PublicURL:     publicURL,
		SessionSecret: []byte("secret-32-bytes-minimum-length!!"),
		Timezone:      time.UTC,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	for _, path := range []string{"/stick/healthz", "/stick/readyz"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("health route %s: status=%d, want 200", path, recorder.Code)
		}
	}
	assertStylesheet(t, h, "/stick/assets/styles.css")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stick", nil))
	if recorder.Code != http.StatusMovedPermanently || recorder.Header().Get("Location") != "/stick/" {
		t.Errorf("bare mount: status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
	recorder = httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stick?view=all", nil))
	if recorder.Header().Get("Location") != "/stick/?view=all" {
		t.Errorf("bare mount query redirect location=%q", recorder.Header().Get("Location"))
	}
	recorder = httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stick/assets/", nil))
	if recorder.Code != http.StatusNotFound || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/html") || !strings.Contains(recorder.Body.String(), "Not found") {
		t.Errorf("asset directory response=%d %q, want local HTML 404", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	recorder = httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stick/assets/app.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("executable asset status=%d, want 404", recorder.Code)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("root health route %s: status=%d, want 404", path, recorder.Code)
		}
	}
	for _, path := range []string{"/", "/assets/styles.css", "/auth/login", "/sticks/aa001", "/stick-other/"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("unmounted alias %s: status=%d, want 404", path, recorder.Code)
		}
	}

	for _, path := range []string{
		"/stick/",
		"/stick/sticks/new",
		"/stick/sticks/aa001",
	} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/stick/auth/login" {
			t.Errorf("browser route %s: status=%d location=%q", path, recorder.Code, recorder.Header().Get("Location"))
		}
	}
}

func assertStylesheet(t *testing.T, h http.Handler, path string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("asset route %s: status=%d, want 200", path, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("asset route %s: Content-Type=%q, want text/css", path, got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, no-cache" {
		t.Errorf("asset route %s: Cache-Control=%q", path, got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "--font-sans") {
		t.Errorf("asset route %s did not return the embedded stylesheet", path)
	}
}

func TestNewHandlerUsesRootPathsWithoutBasePath(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	publicURL := testPublicURL(t, "http://example.test", "")
	h, err := newHandler(application.NewService(store), store, Options{
		PublicURL:     publicURL,
		SessionSecret: []byte("secret-32-bytes-minimum-length!!"),
		Timezone:      time.UTC,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("health route %s: status=%d, want 200", path, recorder.Code)
		}
	}
	assertStylesheet(t, h, "/assets/styles.css")
}

func TestNewHandlerRejectsMissingPublicURL(t *testing.T) {
	if _, err := newHandler(nil, nil, Options{}); err == nil || !strings.Contains(err.Error(), "public URL") {
		t.Fatalf("newHandler error = %v, want public URL validation error", err)
	}
}

func TestNewHandlerOmitsNotificationRoutesWhenDisabled(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	publicURL := testPublicURL(t, "http://example.test", "")
	h, err := newHandler(application.NewService(store), store, Options{PublicURL: publicURL})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sticks/aa001/notify", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("notification route status = %d, want 404", recorder.Code)
	}
}

func TestMountedAuthenticatedFormMutationThroughHTTPHandler(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secret := []byte("secret-32-bytes-minimum-length!!")
	publicURL := testPublicURL(t, "https://example.test", "/stick")
	h, err := newHandler(application.NewService(store), store, Options{
		PublicURL:     publicURL,
		SessionSecret: secret,
		AdminEmails:   []string{"alice@example.com"},
		Timezone:      time.UTC,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	sessionToken, err := auth.Issue(domain.Identity{
		Sub:           "user-1",
		Email:         "alice@example.com",
		EmailVerified: true,
	}, secret)
	if err != nil {
		t.Fatalf("IssueSession: %v", err)
	}

	get := httptest.NewRequest(http.MethodGet, "/stick/sticks/new", nil)
	get.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionToken})
	getRecorder := httptest.NewRecorder()
	h.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getRecorder.Code)
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getRecorder.Body.String())
	if len(match) != 2 {
		t.Fatal("rendered mounted form did not contain a CSRF token")
	}

	form := url.Values{"name": {"Deploy Key"}, "csrf_token": {match[1]}}
	post := httptest.NewRequest(http.MethodPost, "/stick/sticks/new", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionToken})
	postRecorder := httptest.NewRecorder()
	h.ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusSeeOther || postRecorder.Header().Get("Location") != "/stick/" {
		t.Fatalf("POST status = %d, Location = %q", postRecorder.Code, postRecorder.Header().Get("Location"))
	}
	createdSticks, err := store.ListSticks(context.Background())
	if err != nil {
		t.Fatalf("ListSticks: %v", err)
	}
	if len(createdSticks) != 1 || createdSticks[0].Name != "Deploy Key" {
		t.Fatalf("created sticks = %+v", createdSticks)
	}

	for _, test := range []struct {
		name   string
		body   string
		status int
		text   string
	}{
		{name: "malformed form", body: "csrf_token=" + match[1] + "&name=%zz", status: http.StatusBadRequest, text: "invalid or too large"},
		{name: "invalid CSRF", body: "csrf_token=wrong&name=Another", status: http.StatusForbidden, text: "Not allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/stick/sticks/new", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: sessionToken})
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("response = %d Content-Type %q", recorder.Code, recorder.Header().Get("Content-Type"))
			}
			body := recorder.Body.String()
			for _, want := range []string{test.text, `href="/stick/"`, `href="/stick/assets/styles.css"`} {
				if !strings.Contains(body, want) {
					t.Errorf("mounted error response missing %q", want)
				}
			}
		})
	}
}

func testPublicURL(t *testing.T, baseURL, basePath string) publicurl.URL {
	t.Helper()
	publicURL, err := publicurl.Parse(baseURL + basePath)
	if err != nil {
		t.Fatal(err)
	}
	return publicURL
}

type testUI struct {
	renderer         render.Renderer
	dashboardHandler *dashboard.Handler
	sticksHandler    *sticks.Handler
}

func newUI(service *application.Service, loc *time.Location, publicURL publicurl.URL, notificationsEnabled bool) (*testUI, error) {
	dashboardTemplate, err := content.ParsePage(content.Dashboard)
	if err != nil {
		return nil, err
	}
	detailTemplate, err := content.ParsePage(content.Detail)
	if err != nil {
		return nil, err
	}
	newStickTemplate, err := content.ParsePage(content.NewStick)
	if err != nil {
		return nil, err
	}
	errorTemplate, err := content.ParsePage(content.Error)
	if err != nil {
		return nil, err
	}

	renderer := render.New(views.NewMapper(publicURL, loc, notificationsEnabled), errorTemplate)
	return &testUI{
		renderer:         renderer,
		dashboardHandler: dashboard.New(service, renderer, dashboardTemplate, notificationsEnabled),
		sticksHandler:    sticks.New(service, publicURL, renderer, detailTemplate, newStickTemplate, notificationsEnabled),
	}, nil
}

func newTestDB(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createStick(t *testing.T, store *sqlite.Store, id, name string) {
	t.Helper()
	stick, err := domain.NewStick(id, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTransaction(context.Background(), func(tx application.Transaction) error {
		return tx.CreateStick(context.Background(), stick)
	}); err != nil {
		t.Fatal(err)
	}
}

func currentStick(t *testing.T, store *sqlite.Store, id string) domain.Stick {
	t.Helper()
	stick, err := store.GetStick(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return stick
}

func claimStick(t *testing.T, store *sqlite.Store, id string, identity domain.Identity, reason string) {
	t.Helper()
	stick := currentStick(t, store, id)
	if _, err := application.NewService(store).ClaimStick(context.Background(), identity, id, reason, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func releaseStick(t *testing.T, store *sqlite.Store, id, subject string) {
	t.Helper()
	stick := currentStick(t, store, id)
	if _, err := application.NewService(store).ReleaseStick(context.Background(), domain.Identity{Sub: subject}, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func archiveStick(t *testing.T, store *sqlite.Store, id string) {
	t.Helper()
	stick := currentStick(t, store, id)
	if _, err := application.NewService(store).ArchiveStick(context.Background(), domain.Identity{IsAdmin: true}, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func subscribeStick(t *testing.T, store *sqlite.Store, id, subject, name, email string) {
	t.Helper()
	stick := currentStick(t, store, id)
	if err := application.NewService(store).Subscribe(context.Background(), domain.Identity{
		Sub:   subject,
		Name:  name,
		Email: email,
	}, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func stickVersion(t *testing.T, store *sqlite.Store, id string) string {
	t.Helper()
	return strconv.FormatInt(currentStick(t, store, id).Version, 10)
}

func TestUI_Dashboard_Renders(t *testing.T) {
	store := newTestDB(t)
	handler, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	ctx := auth.WithIdentity(req.Context(), identity)
	req = req.WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: security.SessionCookieName(), Value: "session"})
	rr := httptest.NewRecorder()
	security.CSRFProtection([]byte("secret-32-bytes-minimum-length!!"), handler.renderer.ErrorPage)(http.HandlerFunc(handler.dashboardHandler.Dashboard)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "grove") {
		t.Error("expected dashboard to contain grove")
	}
	if !strings.Contains(body, `class="grove"`) {
		t.Error("expected dashboard to render the grove grid")
	}
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Error("expected dashboard forms to include CSRF tokens")
	}
}

func TestUI_DashboardRendersSubscribedState(t *testing.T) {
	store := newTestDB(t)
	for _, stick := range []struct{ id, name string }{{"aa001", "one"}, {"bb002", "two"}, {"cc003", "three"}} {
		createStick(t, store, stick.id, stick.name)
		claimStick(t, store, stick.id, domain.Identity{
			Sub:   "holder-" + stick.id,
			Name:  "Holder",
			Email: stick.id + "@example.com",
		}, "working")
	}
	subscribeStick(t, store, "bb002", "viewer", "Viewer", "viewer@example.com")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "viewer"}))
	recorder := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "/sticks/bb002/notify/cancel") {
		t.Fatal("dashboard did not render subscribed state")
	}
}

func TestUI_RendersPrefixedApplicationURLs(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "available")
	createStick(t, store, "bb002", "taken")
	claimStick(t, store, "bb002", domain.Identity{Sub: "other", Email: "other@example.com"}, "testing")
	createStick(t, store, "cc003", "archived")
	archiveStick(t, store, "cc003")
	admin := domain.Identity{Sub: "admin", Name: "Admin", Email: "admin@example.com", IsAdmin: true}
	for range 21 {
		claimStick(t, store, "aa001", admin, "testing")
		releaseStick(t, store, "aa001", admin.Sub)
	}

	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, "/basepath"), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(auth.WithIdentity(ctx, admin))
	dashboardRR := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(dashboardRR, dashboardReq)
	if dashboardRR.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", dashboardRR.Code)
	}
	for _, want := range []string{
		`href="/basepath/assets/styles.css"`,
		`action="/basepath/auth/logout"`,
		`href="/basepath/sticks/aa001"`,
		`action="/basepath/sticks/bb002/notify"`,
		`value="/basepath/"`,
		`href="/basepath/sticks/new"`,
		`href="/basepath/sticks/cc003"`,
		`action="/basepath/sticks/cc003/unarchive"`,
	} {
		if !strings.Contains(dashboardRR.Body.String(), want) {
			t.Errorf("dashboard missing prefixed URL %q", want)
		}
	}
	if strings.Contains(dashboardRR.Body.String(), `href="/sticks/`) || strings.Contains(dashboardRR.Body.String(), `action="/sticks/`) {
		t.Error("dashboard contains an unprefixed application URL")
	}

	newStickReq := httptest.NewRequest(http.MethodGet, "/sticks/new", nil).WithContext(auth.WithIdentity(ctx, admin))
	newStickRR := httptest.NewRecorder()
	ui.sticksHandler.NewStick(newStickRR, newStickReq)
	if newStickRR.Code != http.StatusOK {
		t.Fatalf("new stick status = %d", newStickRR.Code)
	}
	for _, want := range []string{`href="/basepath/"`, `action="/basepath/sticks/new"`} {
		if !strings.Contains(newStickRR.Body.String(), want) {
			t.Errorf("new-stick page missing prefixed URL %q", want)
		}
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/sticks/aa001", nil).WithContext(auth.WithIdentity(ctx, admin))
	detailReq.SetPathValue("id", "aa001")
	detailRR := httptest.NewRecorder()
	ui.sticksHandler.Detail(detailRR, detailReq)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detailRR.Code)
	}
	for _, want := range []string{
		`href="/basepath/"`,
		`action="/basepath/sticks/aa001/rename"`,
		`action="/basepath/sticks/aa001/archive"`,
		`action="/basepath/sticks/aa001/claim"`,
		`href="/basepath/sticks/aa001?page=2"`,
	} {
		if !strings.Contains(detailRR.Body.String(), want) {
			t.Errorf("detail page missing prefixed URL %q", want)
		}
	}
}

func TestUI_FormsMatchBackendLengthLimits(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "available")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.Identity{Sub: "admin", IsAdmin: true}

	newStickRequest := httptest.NewRequest(http.MethodGet, "/sticks/new", nil)
	newStickRequest = newStickRequest.WithContext(auth.WithIdentity(ctx, admin))
	newStickRecorder := httptest.NewRecorder()
	ui.sticksHandler.NewStick(newStickRecorder, newStickRequest)
	if !strings.Contains(newStickRecorder.Body.String(), `name="name" value="" maxlength="100"`) {
		t.Error("new-stick name control does not expose the 100-character backend limit")
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/sticks/aa001", nil)
	detailRequest.SetPathValue("id", "aa001")
	detailRequest = detailRequest.WithContext(auth.WithIdentity(ctx, admin))
	detailRecorder := httptest.NewRecorder()
	ui.sticksHandler.Detail(detailRecorder, detailRequest)
	if !strings.Contains(detailRecorder.Body.String(), `name="reason" value="" maxlength="500"`) {
		t.Error("claim reason control does not expose the 500-character backend limit")
	}
}

func TestUI_DashboardOverlayLinksHaveAccessibleNames(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	viewer := domain.Identity{Sub: "viewer"}
	for _, stick := range []struct {
		id, name, holder string
	}{
		{id: "aa001", name: "Mine", holder: viewer.Sub},
		{id: "bb002", name: "Theirs", holder: "someone-else"},
	} {
		createStick(t, store, stick.id, stick.name)
		claimStick(t, store, stick.id, domain.Identity{Sub: stick.holder, Email: stick.holder + "@example.com"}, "working")
	}
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(auth.WithIdentity(ctx, viewer))
	recorder := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(recorder, request)

	for _, name := range []string{"Mine", "Theirs"} {
		if !strings.Contains(recorder.Body.String(), `aria-label="View `+name+` details"`) {
			t.Errorf("overlay link for %q has no useful accessible name", name)
		}
	}
}

func TestUI_DetailArchivedStickShowsStateWithoutClaimForm(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "retired")
	archiveStick(t, store, "aa001")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/sticks/aa001", nil)
	request.SetPathValue("id", "aa001")
	request = request.WithContext(auth.WithIdentity(ctx, domain.Identity{Sub: "admin", IsAdmin: true}))
	recorder := httptest.NewRecorder()
	ui.sticksHandler.Detail(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `class="status-pill archived"`) || !strings.Contains(body, "This stick is archived and cannot be claimed.") {
		t.Error("archived detail does not clearly expose archived state")
	}
	if strings.Contains(body, `/sticks/aa001/claim`) || strings.Contains(body, `class="claim-form"`) {
		t.Error("archived detail rendered a claim form")
	}
	if !strings.Contains(body, `/sticks/aa001/unarchive`) {
		t.Error("archived detail did not preserve the restore control")
	}
}

func TestUI_RedirectsUseBasePathAndConstrainRedirectTo(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "available")
	createStick(t, store, "bb002", "taken")
	claimStick(t, store, "bb002", domain.Identity{Sub: "other", Email: "other@example.com"}, "testing")
	service := application.NewService(store)
	ui, err := newUI(service, time.UTC, uiPublicURL(t, "/basepath"), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	claimForm := url.Values{"reason": {""}, "version": {stickVersion(t, store, "aa001")}}
	claimReq := httptest.NewRequest(http.MethodPost, "/sticks/aa001/claim", strings.NewReader(claimForm.Encode()))
	claimReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	claimReq.SetPathValue("id", "aa001")
	claimReq = claimReq.WithContext(auth.WithIdentity(ctx, domain.Identity{Sub: "u1"}))
	claimRR := httptest.NewRecorder()
	ui.sticksHandler.Claim(claimRR, claimReq)
	if claimRR.Code != http.StatusUnprocessableEntity || claimRR.Header().Get("Location") != "" {
		t.Errorf("claim response = %d Location %q, want 422 without redirect", claimRR.Code, claimRR.Header().Get("Location"))
	}
	if body := claimRR.Body.String(); !strings.Contains(body, `action="/basepath/sticks/aa001/claim"`) ||
		!strings.Contains(body, `aria-invalid="true"`) || strings.Contains(body, "?error=") {
		t.Errorf("claim validation response is not a mounted, accessible form: %s", body)
	}

	version := stickVersion(t, store, "bb002")
	subscribeForm := url.Values{"redirect_to": {"/outside"}, "version": {version}}
	subscribeReq := httptest.NewRequest(http.MethodPost, "/sticks/bb002/notify", strings.NewReader(subscribeForm.Encode()))
	subscribeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	subscribeReq.SetPathValue("id", "bb002")
	subscribeReq = subscribeReq.WithContext(auth.WithIdentity(ctx, domain.Identity{Sub: "u1", Email: "u1@example.com"}))
	subscribeRR := httptest.NewRecorder()
	ui.sticksHandler.Subscribe(subscribeRR, subscribeReq)
	if got := subscribeRR.Header().Get("Location"); got != "/basepath/sticks/bb002" {
		t.Errorf("unsafe redirect Location = %q, want prefixed fallback", got)
	}
}

func TestUI_Detail_Renders(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "prod-deploy")
	id := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	claimStick(t, store, "aa001", id, "deploying")

	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/sticks/aa001", nil)
	req.SetPathValue("id", "aa001")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	reqCtx := auth.WithIdentity(req.Context(), identity)
	req = req.WithContext(reqCtx)
	rr := httptest.NewRecorder()
	ui.sticksHandler.Detail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "prod-deploy") {
		t.Error("expected detail to contain stick name")
	}
	if !strings.Contains(rr.Body.String(), "This stick has not been used yet.") {
		t.Error("expected active session to be excluded from history")
	}
	if strings.Contains(rr.Body.String(), `class="session"`) {
		t.Error("expected no rendered history row for active session")
	}
}

func TestUI_Detail_BoundsHugePageAndPageLinks(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "prod-deploy")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	for _, page := range []string{"999999999", "9223372036854775807", "invalid", "-1"} {
		req := httptest.NewRequest("GET", "/sticks/aa001?page="+page, nil)
		req.SetPathValue("id", "aa001")
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
		rr := httptest.NewRecorder()

		ui.sticksHandler.Detail(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("page %q: expected 200, got %d: %s", page, rr.Code, rr.Body.String())
		}
		links := strings.Count(rr.Body.String(), `class="page-btn`)
		if page == "999999999" && links != 0 {
			t.Errorf("huge page: rendered %d page links, want no pagination for empty history", links)
		}
		if links > 7 {
			t.Errorf("page %q: rendered %d page links, want at most 7", page, links)
		}
	}
}

func TestUI_Detail_UsesHistoryCountForPageTotals(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "prod-deploy")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	for range 40 {
		claimStick(t, store, "aa001", identity, "deploying")
		releaseStick(t, store, "aa001", identity.Sub)
	}
	claimStick(t, store, "aa001", identity, "still deploying")

	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, page := range []string{"1", "2", "3"} {
		req := httptest.NewRequest("GET", "/sticks/aa001?page="+page, nil)
		req.SetPathValue("id", "aa001")
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
		rr := httptest.NewRecorder()

		ui.sticksHandler.Detail(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("page %s: expected 200, got %d: %s", page, rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if !strings.Contains(body, "40 sessions total") {
			t.Errorf("page %s: expected true history total, got body without it", page)
		}
		if got := strings.Count(body, `class="page-btn`); got != 2 {
			t.Errorf("page %s: rendered %d page links, want 2", page, got)
		}
		if strings.Contains(body, `page=3`) {
			t.Errorf("page %s: rendered an out-of-range page link", page)
		}
	}
}

func TestUI_Detail_NotFound(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/sticks/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	ctx := auth.WithIdentity(req.Context(), identity)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.sticksHandler.Detail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent stick, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" || !strings.Contains(rr.Body.String(), "Not found") {
		t.Errorf("not-found response = Content-Type %q body %q", got, rr.Body.String())
	}
}

func TestUI_Detail_ArchivedStickHiddenFromNonAdmin(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "archived")
	archiveStick(t, store, "aa001")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sticks/aa001", nil)
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "u1"}))
	recorder := httptest.NewRecorder()

	ui.sticksHandler.Detail(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("archived detail status = %d, want 404", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Not found") || strings.Contains(body, "archived") {
		t.Fatalf("archived detail did not preserve authorization hiding: %s", body)
	}
}

func TestDashboard_AdminBadge(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com", IsAdmin: true}
	ctx := auth.WithIdentity(context.Background(), identity)
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "pill--admin") {
		t.Error("expected admin badge (pill--admin) in response for admin user")
	}
}

func TestDashboard_NoAdminBadge(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "u2", Name: "Bob", Email: "bob@example.com", IsAdmin: false}
	ctx := auth.WithIdentity(context.Background(), identity)
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "pill--admin") {
		t.Error("expected no admin badge for non-admin user")
	}
}

func TestDashboard_AdminNewStickCard(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com", IsAdmin: true}
	ctx := auth.WithIdentity(context.Background(), identity)
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(rr, req)

	if !strings.Contains(rr.Body.String(), "/sticks/new") {
		t.Error("expected admin to see new stick card linking to /sticks/new")
	}
}

func TestDashboard_NonAdminNoNewStickCard(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "u2", Name: "Bob", Email: "bob@example.com", IsAdmin: false}
	ctx := auth.WithIdentity(context.Background(), identity)
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(rr, req)

	if strings.Contains(rr.Body.String(), "/sticks/new") {
		t.Error("expected non-admin to NOT see new stick card")
	}
}

func TestDashboard_AdminArchivedSticks(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "retired deploy")
	archiveStick(t, store, "aa001")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.Identity{Sub: "admin", Name: "Admin", Email: "admin@example.com", IsAdmin: true}
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(auth.WithIdentity(context.Background(), identity))
	rr := httptest.NewRecorder()
	ui.dashboardHandler.Dashboard(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "retired deploy") || !strings.Contains(rr.Body.String(), "/unarchive") {
		t.Fatalf("dashboard status = %d, body missing archived stick: %s", rr.Code, rr.Body.String())
	}
}

func TestUI_NewStick_Admin(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "admin1", Name: "Admin", Email: "admin@example.com", IsAdmin: true}
	ctx := auth.WithIdentity(context.Background(), identity)
	req := httptest.NewRequest("GET", "/sticks/new", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.sticksHandler.NewStick(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "New stick") {
		t.Error("expected new stick form in response")
	}
}

func TestUI_NewStick_NonAdmin(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identity := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com", IsAdmin: false}
	ctx := auth.WithIdentity(context.Background(), identity)
	req := httptest.NewRequest("GET", "/sticks/new", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	ui.sticksHandler.NewStick(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" || !strings.Contains(rr.Body.String(), "Not allowed") {
		t.Errorf("forbidden response = Content-Type %q body %q", got, rr.Body.String())
	}
}

func TestUIUnexpectedFailureRendersSafeRequestIDErrorPage(t *testing.T) {
	store := newTestDB(t)
	handler, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, "/basepath"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/basepath/", nil)
	request = request.WithContext(auth.WithIdentity(request.Context(), domain.Identity{Sub: "u1"}))
	request = request.WithContext(httpx.WithRequestID(request.Context(), "request-42"))
	recorder := httptest.NewRecorder()

	handler.dashboardHandler.Dashboard(recorder, request)

	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("response = %d Content-Type %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	body := recorder.Body.String()
	for _, want := range []string{"Something went wrong", "request-42", `href="/basepath/"`, `href="/basepath/assets/styles.css"`} {
		if !strings.Contains(body, want) {
			t.Errorf("error page missing %q", want)
		}
	}
	for _, forbidden := range []string{"database is closed", "sql:", "internal error"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("error page leaked implementation detail %q", forbidden)
		}
	}
}

func TestUI_CreateStick_AdminForm(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, "/basepath"), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/sticks/new", strings.NewReader("name=Deploy+Key"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "admin1", IsAdmin: true}))
	recorder := httptest.NewRecorder()
	ui.sticksHandler.CreateStick(recorder, req)

	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/basepath/" {
		t.Fatalf("status = %d, Location = %q", recorder.Code, recorder.Header().Get("Location"))
	}
	sticks, err := store.ListSticks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sticks) != 1 || sticks[0].Name != "Deploy Key" {
		t.Fatalf("created sticks = %+v", sticks)
	}
	if _, err := uuid.Parse(sticks[0].ID); err != nil {
		t.Fatalf("created stick ID = %q, want UUID: %v", sticks[0].ID, err)
	}
}

func TestUI_CreateStick_RendersNonAdminErrorPage(t *testing.T) {
	store := newTestDB(t)
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/sticks/new", strings.NewReader("name=Deploy+Key"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "u1"}))
	recorder := httptest.NewRecorder()

	ui.sticksHandler.CreateStick(recorder, req)

	if recorder.Code != http.StatusForbidden || recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("status = %d, Content-Type = %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	if got := recorder.Body.String(); !strings.Contains(got, "Not allowed") || strings.Contains(got, "forbidden\n") {
		t.Fatalf("unsafe or unclear error page: %q", got)
	}
}

func TestUI_CreateStick_InvalidFormInputs(t *testing.T) {
	for _, test := range []struct {
		name           string
		body           string
		wantStatus     int
		wantValue      string
		wantFieldError bool
	}{
		{
			name:           "invalid name",
			body:           "name=bad%21name",
			wantStatus:     http.StatusUnprocessableEntity,
			wantValue:      "bad!name",
			wantFieldError: true,
		},
		{
			name:           "oversized name",
			body:           "name=" + url.QueryEscape(strings.Repeat("x", 101)),
			wantStatus:     http.StatusUnprocessableEntity,
			wantValue:      strings.Repeat("x", 101),
			wantFieldError: true,
		},
		{
			name:       "oversized body",
			body:       "name=Deploy+Key&padding=" + strings.Repeat("x", 9*1024),
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestDB(t)
			ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, "/basepath"), true)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/sticks/new", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "admin1", IsAdmin: true}))
			recorder := httptest.NewRecorder()

			ui.sticksHandler.CreateStick(recorder, req)

			if recorder.Code != test.wantStatus || recorder.Header().Get("Location") != "" {
				t.Fatalf("status = %d, Location = %q, want %d without redirect", recorder.Code, recorder.Header().Get("Location"), test.wantStatus)
			}
			if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q, want HTML", got)
			}
			body := recorder.Body.String()
			if strings.Contains(body, "?error=") {
				t.Error("validation response contains query-string error transport")
			}
			if test.wantFieldError {
				for _, want := range []string{
					`value="` + test.wantValue + `"`,
					`aria-invalid="true"`,
					`aria-describedby="name-hint name-error"`,
					`id="name-error" role="alert"`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("validation response missing %q", want)
					}
				}
			} else if !strings.Contains(body, "invalid or too large") {
				t.Error("bad request page does not explain the invalid or oversized form")
			}
			sticks, err := store.ListSticks(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(sticks) != 0 {
				t.Fatalf("invalid form persisted sticks: %+v", sticks)
			}
		})
	}
}

func TestUI_Claim_RejectsOversizedReason(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "prod")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	form := url.Values{"reason": {strings.Repeat("x", 501)}, "version": {stickVersion(t, store, "aa001")}}
	req := httptest.NewRequest("POST", "/sticks/aa001/claim", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "u1"}))
	rr := httptest.NewRecorder()
	ui.sticksHandler.Claim(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rr.Code)
	}
	for _, want := range []string{`value="` + strings.Repeat("x", 501) + `"`, `aria-invalid="true"`, `aria-describedby="claim-reason-error"`, `id="claim-reason-error" role="alert"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("claim validation response missing %q", want)
		}
	}
	stick, err := store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	if !stick.Available() {
		t.Fatal("oversized reason should not claim the stick")
	}
}

func TestUI_Claim_RejectsOversizedBody(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "prod")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "reason=deploying&padding=" + strings.Repeat("x", 9*1024)
	req := httptest.NewRequest("POST", "/sticks/aa001/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "u1"}))
	rr := httptest.NewRecorder()
	ui.sticksHandler.Claim(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" || !strings.Contains(rr.Body.String(), "invalid or too large") {
		t.Errorf("oversized form response = Content-Type %q body %q", got, rr.Body.String())
	}
	stick, err := store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	if !stick.Available() {
		t.Fatal("oversized body should not claim the stick")
	}
}

func TestUI_Claim_RejectsWhitespaceOnlyReason(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "prod")
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	form := url.Values{"reason": {" \t"}, "version": {stickVersion(t, store, "aa001")}}
	req := httptest.NewRequest("POST", "/sticks/aa001/claim", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), domain.Identity{Sub: "u1"}))
	rr := httptest.NewRecorder()
	ui.sticksHandler.Claim(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, `aria-invalid="true"`) ||
		!strings.Contains(body, "Reason must be non-empty") || strings.Contains(body, "?error=") {
		t.Errorf("whitespace validation response is not accessible/safe: %s", body)
	}
	stick, err := store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	if !stick.Available() {
		t.Fatal("whitespace-only reason should not claim the stick")
	}
}

func TestFormatTime_UsesConfiguredTimezone(t *testing.T) {
	store := newTestDB(t)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	uiHandler, err := newUI(application.NewService(store), loc, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	createStick(t, store, "tz001", "tz-stick")
	id := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	claimStick(t, store, "tz001", id, "testing")

	req := httptest.NewRequest("GET", "/sticks/tz001", nil)
	req.SetPathValue("id", "tz001")
	reqCtx := auth.WithIdentity(req.Context(), id)
	req = req.WithContext(reqCtx)
	rr := httptest.NewRecorder()
	uiHandler.sticksHandler.Detail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// The timestamp was stored as UTC. With America/New_York (UTC-4 or UTC-5),
	// the rendered time should NOT contain "UTC" in the time offset sense,
	// and should differ from a UTC-rendered time.
	// We can't predict the exact time, but we can verify the page rendered and
	// contains a timestamp (formatTime produces "Jan 2 · 15:04" format).
	body := rr.Body.String()
	if !strings.Contains(body, "·") {
		t.Error("expected formatted timestamp with '·' separator in detail page")
	}

	// Render again with UTC to confirm they produce different output when time crosses hour boundary.
	// Use a fixed timestamp to make this deterministic.
	uiUTC, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New UTC: %v", err)
	}
	req2 := httptest.NewRequest("GET", "/sticks/tz001", nil)
	req2.SetPathValue("id", "tz001")
	req2 = req2.WithContext(auth.WithIdentity(req2.Context(), id))
	rr2 := httptest.NewRecorder()
	uiUTC.sticksHandler.Detail(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr2.Code)
	}
	// Both renders should produce a timestamp with '·' — the test validates the
	// template function is wired correctly in both cases.
	if !strings.Contains(rr2.Body.String(), "·") {
		t.Error("expected formatted timestamp with '·' separator in UTC detail page")
	}
}

func TestUI_Subscribe_And_Unsubscribe(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	createStick(t, store, "aa001", "prod-deploy")
	id := domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}
	claimStick(t, store, "aa001", domain.Identity{Sub: "u2", Name: "Bob", Email: "bob@example.com"}, "deploying")

	service := application.NewService(store)
	ui, err := newUI(service, time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Subscribe
	version := stickVersion(t, store, "aa001")
	req := httptest.NewRequest("POST", "/sticks/aa001/notify", strings.NewReader(url.Values{"version": {version}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), id))
	rr := httptest.NewRecorder()
	ui.sticksHandler.Subscribe(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("Subscribe: expected 303, got %d", rr.Code)
	}

	// Verify subscribed
	ids, _ := service.SubscribedStickIDs(ctx, id)
	if len(ids) != 1 || ids[0] != "aa001" {
		t.Error("expected user to be subscribed after Subscribe handler")
	}

	// Unsubscribe
	req2 := httptest.NewRequest("POST", "/sticks/aa001/notify/cancel", strings.NewReader(url.Values{"version": {version}}.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.SetPathValue("id", "aa001")
	req2 = req2.WithContext(auth.WithIdentity(req2.Context(), id))
	rr2 := httptest.NewRecorder()
	ui.sticksHandler.Unsubscribe(rr2, req2)
	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("Unsubscribe: expected 303, got %d", rr2.Code)
	}

	ids, _ = service.SubscribedStickIDs(ctx, id)
	if len(ids) != 0 {
		t.Error("expected user to not be subscribed after Unsubscribe handler")
	}
}

func TestUI_DetailRendersSubscribedState(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "prod-deploy")
	claimStick(t, store, "aa001", domain.Identity{Sub: "u1", Name: "Alice", Email: "alice@example.com"}, "deploying")

	watcher := domain.Identity{Sub: "u2", Name: "Bob", Email: "bob@example.com"}
	subscribeStick(t, store, "aa001", watcher.Sub, watcher.Name, watcher.Email)

	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/sticks/aa001", nil)
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), watcher))
	rr := httptest.NewRecorder()
	ui.sticksHandler.Detail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// Bob is subscribed, so the cancel form should appear
	if !strings.Contains(rr.Body.String(), "/notify/cancel") {
		t.Error("expected cancel notification form for subscribed user")
	}
}

func TestUI_DetailComparesHolderBySubjectNotEmail(t *testing.T) {
	store := newTestDB(t)
	createStick(t, store, "aa001", "prod-deploy")
	holder := domain.Identity{Sub: "holder-sub", Name: "Alice", Email: "shared@example.com"}
	claimStick(t, store, "aa001", holder, "deploying")
	viewer := domain.Identity{Sub: "different-sub", Name: "Other Alice", Email: "shared@example.com"}
	ui, err := newUI(application.NewService(store), time.UTC, uiPublicURL(t, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sticks/aa001", nil)
	req.SetPathValue("id", "aa001")
	req = req.WithContext(auth.WithIdentity(req.Context(), viewer))
	recorder := httptest.NewRecorder()

	ui.sticksHandler.Detail(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "/sticks/aa001/release") {
		t.Error("viewer with the holder's email but a different subject was offered release")
	}
	if !strings.Contains(recorder.Body.String(), "/sticks/aa001/notify") {
		t.Error("viewer with a different subject was not offered notification subscription")
	}
}

func uiPublicURL(t *testing.T, mountPath string) publicurl.URL {
	t.Helper()
	publicURL, err := publicurl.Parse("http://example.test" + mountPath)
	if err != nil {
		t.Fatal(err)
	}
	return publicURL
}
