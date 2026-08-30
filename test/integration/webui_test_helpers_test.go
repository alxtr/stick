package integration_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"stick/internal/adapters/persistence/sqlite"
	"stick/internal/application"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/content"
	"stick/internal/web/render"
	"stick/internal/web/sticks"
	"stick/internal/web/views"
)

func newSticksHandler(service *application.Service, loc *time.Location, publicURL publicurl.URL, notificationsEnabled bool) (*sticks.Handler, render.Renderer, error) {
	renderer, err := newWebUIRenderer(loc, publicURL, notificationsEnabled)
	if err != nil {
		return nil, render.Renderer{}, err
	}
	detailTemplate, err := content.ParsePage(content.Detail)
	if err != nil {
		return nil, render.Renderer{}, err
	}
	newStickTemplate, err := content.ParsePage(content.NewStick)
	if err != nil {
		return nil, render.Renderer{}, err
	}
	return sticks.New(service, publicURL, renderer, detailTemplate, newStickTemplate, notificationsEnabled), renderer, nil
}

func newWebUIRenderer(loc *time.Location, publicURL publicurl.URL, notificationsEnabled bool) (render.Renderer, error) {
	errorTemplate, err := content.ParsePage(content.Error)
	if err != nil {
		return render.Renderer{}, err
	}
	renderer := render.New(views.NewMapper(publicURL, loc, notificationsEnabled), errorTemplate)
	return renderer, nil
}

func webuiPublicURL(t *testing.T, mountPath string) publicurl.URL {
	t.Helper()
	publicURL, err := publicurl.Parse("http://example.test" + mountPath)
	if err != nil {
		t.Fatal(err)
	}
	return publicURL
}

func newWebUITestDB(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createWebUIStick(t *testing.T, store *sqlite.Store, id, name string) {
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

func currentWebUIStick(t *testing.T, store *sqlite.Store, id string) domain.Stick {
	t.Helper()
	stick, err := store.GetStick(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return stick
}

func claimWebUIStick(t *testing.T, store *sqlite.Store, id string, identity domain.Identity, reason string) {
	t.Helper()
	stick := currentWebUIStick(t, store, id)
	if _, err := application.NewService(store).ClaimStick(context.Background(), identity, id, reason, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func archiveWebUIStick(t *testing.T, store *sqlite.Store, id string) {
	t.Helper()
	stick := currentWebUIStick(t, store, id)
	if _, err := application.NewService(store).ArchiveStick(context.Background(), domain.Identity{IsAdmin: true}, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func subscribeWebUIStick(t *testing.T, store *sqlite.Store, id, subject, name, email string) {
	t.Helper()
	stick := currentWebUIStick(t, store, id)
	if err := application.NewService(store).Subscribe(context.Background(), domain.Identity{
		Sub:   subject,
		Name:  name,
		Email: email,
	}, id, stick.Version); err != nil {
		t.Fatal(err)
	}
}

func webUIStickVersion(t *testing.T, store *sqlite.Store, id string) string {
	t.Helper()
	return strconv.FormatInt(currentWebUIStick(t, store, id).Version, 10)
}
