package integration_test

import (
	"context"
	"html"
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
)

func TestUIFormsCarryVersionAndRejectStaleSubmission(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	stick, err := domain.NewStick("aa001", "original")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTransaction(ctx, func(tx application.Transaction) error {
		return tx.CreateStick(ctx, stick)
	}); err != nil {
		t.Fatal(err)
	}
	publicURL, err := publicurl.Parse("http://example.test/base")
	if err != nil {
		t.Fatal(err)
	}
	handler, _, err := newSticksHandler(application.NewService(store), time.UTC, publicURL, true)
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.Identity{Sub: "admin", IsAdmin: true}
	detail := httptest.NewRequest(http.MethodGet, "/sticks/aa001", nil)
	detail.SetPathValue("id", "aa001")
	detail = detail.WithContext(auth.WithIdentity(detail.Context(), admin))
	detailRecorder := httptest.NewRecorder()
	handler.Detail(detailRecorder, detail)
	match := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(detailRecorder.Body.String())
	if len(match) != 2 {
		t.Fatalf("detail form did not carry a version")
	}
	stale := html.UnescapeString(match[1])
	current, err := store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewService(store).RenameStick(ctx, admin, "aa001", "newer", current.Version); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"name": {"stale overwrite"}, "version": {stale}}
	request := httptest.NewRequest(http.MethodPost, "/sticks/aa001/rename", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("id", "aa001")
	request = request.WithContext(auth.WithIdentity(request.Context(), admin))
	recorder := httptest.NewRecorder()
	handler.Rename(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale UI status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location != "" {
		t.Fatalf("stale response redirected to %q", location)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "changed since") || !strings.Contains(body, "newer") {
		t.Fatalf("stale response did not render current state: %s", body)
	}
	freshVersion := html.EscapeString(stickVersionForFormTest(t, store, "aa001"))
	if !strings.Contains(body, `name="version" value="`+freshVersion+`"`) {
		t.Fatalf("stale response missing fresh version %q", freshVersion)
	}
	if strings.Contains(body, "?error=") {
		t.Fatal("stale response used query-string error transport")
	}
	stick, err = store.GetStick(ctx, "aa001")
	if err != nil {
		t.Fatal(err)
	}
	if stick.Name != "newer" || stick.Version != 2 {
		t.Fatalf("stale UI request changed stick: %+v", stick)
	}
}

func stickVersionForFormTest(t *testing.T, store *sqlite.Store, id string) string {
	t.Helper()
	stick, err := store.GetStick(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(stick.Version, 10)
}
