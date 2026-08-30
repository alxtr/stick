package views

import (
	"net/http/httptest"
	"testing"
	"time"

	domain "stick/internal/core"
	"stick/internal/publicurl"
)

func TestDashboardViewModelMapsIdentityAndStickActions(t *testing.T) {
	now := time.Date(2026, time.August, 27, 18, 0, 0, 0, time.UTC)
	claimedAt := now.Add(-time.Hour - 35*time.Minute)
	archivedAt := now.Add(-24 * time.Hour)
	mapper := testViewModelMapper(t, time.UTC, now)
	identity := domain.Identity{
		Sub:     "viewer-sub",
		Email:   "viewer@example.com",
		IsAdmin: true,
	}
	sticks := []domain.Stick{
		{ID: "available", Name: "Available", Version: 3},
		{
			ID:      "mine",
			Name:    "Mine",
			Version: 4,
			Holder: &domain.Holder{
				Sub:       "viewer-sub",
				Email:     "viewer@example.com",
				Reason:    "deploying",
				ClaimedAt: claimedAt,
			},
		},
		{
			ID:      "theirs",
			Name:    "Theirs",
			Version: 5,
			Holder: &domain.Holder{
				Sub:       "another-sub",
				Email:     "holder@example.com",
				Reason:    "testing",
				ClaimedAt: claimedAt,
			},
		},
	}
	archived := []domain.Stick{{
		ID:         "retired",
		Name:       "Retired",
		Version:    6,
		ArchivedAt: &archivedAt,
	}}
	request := httptest.NewRequest("GET", "/", nil)

	view := mapper.Dashboard(request, identity, sticks, []string{"theirs"}, archived)

	if view.CurrentUser.Email != identity.Email || !view.CurrentUser.ShowAdminBadge {
		t.Fatalf("current user view = %+v", view.CurrentUser)
	}
	if !view.CanCreateStick || view.NewStickURL != "/base/sticks/new" {
		t.Fatalf("create-stick presentation = can create %t, URL %q", view.CanCreateStick, view.NewStickURL)
	}
	if view.HomeURL != "/base/" || view.StylesheetURL != "/base/assets/styles.css" || view.LogoutAction != "/base/auth/logout" {
		t.Fatalf("mounted page URLs = home %q, stylesheet %q, logout %q", view.HomeURL, view.StylesheetURL, view.LogoutAction)
	}
	if len(view.Sticks) != 3 {
		t.Fatalf("stick views = %d, want 3", len(view.Sticks))
	}

	available := view.Sticks[0]
	if !available.CanClaim || available.HeldByMe || available.HeldByOther {
		t.Fatalf("available stick view = %+v", available)
	}
	mine := view.Sticks[1]
	if !mine.HeldByMe || mine.HeldDuration != "1h 35min" || mine.ActionForm.Action != "/base/sticks/mine/release" {
		t.Fatalf("held-by-me stick view = %+v", mine)
	}
	theirs := view.Sticks[2]
	if !theirs.HeldByOther || theirs.ActionForm == nil {
		t.Fatalf("subscribed held-by-other stick view = %+v", theirs)
	}
	if theirs.DetailURL != "/base/sticks/theirs" || theirs.ActionForm.Action != "/base/sticks/theirs/notify/cancel" {
		t.Fatalf("held-by-other URLs = detail %q, action %q", theirs.DetailURL, theirs.ActionForm.Action)
	}
	if !view.ShowArchivedSticks || len(view.ArchivedSticks) != 1 || view.ArchivedSticks[0].RestoreForm == nil {
		t.Fatalf("archived stick presentation = show %t, views %+v", view.ShowArchivedSticks, view.ArchivedSticks)
	}
}

func TestDetailViewModelMapsStateActionsAndFormattedHistory(t *testing.T) {
	now := time.Date(2026, time.August, 27, 18, 0, 0, 0, time.UTC)
	location := time.FixedZone("configured", -4*60*60)
	mapper := testViewModelMapper(t, location, now)
	claimedAt := time.Date(2026, time.August, 27, 16, 5, 0, 0, time.UTC)
	releasedAt := claimedAt.Add(time.Hour + 5*time.Minute)
	held := domain.Stick{
		ID:      "held",
		Name:    "Held",
		Version: 8,
		Holder: &domain.Holder{
			Sub:       "holder-sub",
			Email:     "shared@example.com",
			Reason:    "shipping",
			ClaimedAt: claimedAt,
		},
	}

	mine := mapper.StickDetail(held, domain.Identity{Sub: "holder-sub"}, false, now, "csrf-token", DetailFormState{})
	if !mine.HeldByMe || mine.StateActionForm.Action != "/base/sticks/held/release" || mine.StatusLabel != "yours" || mine.StatusClass != "mine" {
		t.Fatalf("holder detail view = %+v", mine)
	}
	if mine.HeldSince != "Aug 27 · 12:05" || mine.HeldDuration != "1h 55min" {
		t.Fatalf("formatted hold = since %q, duration %q", mine.HeldSince, mine.HeldDuration)
	}

	other := mapper.StickDetail(held, domain.Identity{Sub: "different-sub", Email: "shared@example.com"}, true, now, "csrf-token", DetailFormState{})
	if !other.HeldByOther || other.HeldByMe || other.StateActionForm.Action != "/base/sticks/held/notify/cancel" {
		t.Fatalf("non-holder detail view = %+v", other)
	}

	available := domain.Stick{ID: "available", Name: "Available", Version: 2}
	adminAvailable := mapper.StickDetail(available, domain.Identity{IsAdmin: true}, false, now, "csrf-token", DetailFormState{})
	if !adminAvailable.CanClaim || !adminAvailable.CanRename || !adminAvailable.ShowAdminControls || adminAvailable.AdminActionForm.Action != "/base/sticks/available/archive" {
		t.Fatalf("admin available detail view = %+v", adminAvailable)
	}
	archivedAt := now.Add(-time.Hour)
	archived := available
	archived.ArchivedAt = &archivedAt
	adminArchived := mapper.StickDetail(archived, domain.Identity{IsAdmin: true}, false, now, "csrf-token", DetailFormState{})
	if !adminArchived.IsArchived || adminArchived.CanClaim || adminArchived.AdminActionForm.Action != "/base/sticks/available/unarchive" || adminArchived.StatusLabel != "archived" {
		t.Fatalf("admin archived detail view = %+v", adminArchived)
	}

	request := httptest.NewRequest("GET", "/sticks/available?page=2", nil)
	renameValue := "bad/name"
	view := mapper.Detail(
		request,
		domain.Identity{Sub: "viewer"},
		available,
		[]domain.Session{{
			HolderEmail: "holder@example.com",
			Reason:      "deploying",
			ClaimedAt:   claimedAt,
			ReleasedAt:  &releasedAt,
		}},
		2,
		3,
		41,
		[]int{1, 2, 3},
		nil,
		DetailFormState{
			Alert:            "presentation error",
			RenameValue:      &renameValue,
			RenameError:      "rename error",
			ClaimReasonValue: "submitted reason",
			ClaimReasonError: "claim error",
		},
	)
	if len(view.Sessions) == 0 || len(view.PageLinks) == 0 || view.TotalSessionsLabel != "41 sessions total" || view.Error != "presentation error" {
		t.Fatalf("detail page presentation = %+v", view)
	}
	if got := view.Sessions[0]; got.Duration != "1h 05min" || got.TimeRange != "Aug 27 · 12:05 → Aug 27 · 13:10" {
		t.Fatalf("history session view = %+v", got)
	}
	if len(view.PageLinks) != 3 || !view.PageLinks[1].IsCurrent || view.PageLinks[1].URL != "/base/sticks/available?page=2" {
		t.Fatalf("pagination views = %+v", view.PageLinks)
	}
	if view.Stick.RenameField.Value != renameValue || !view.Stick.RenameField.HasError ||
		view.Stick.ClaimReasonField.Value != "submitted reason" || !view.Stick.ClaimReasonField.HasError {
		t.Fatalf("submitted field presentation = rename %+v claim %+v", view.Stick.RenameField, view.Stick.ClaimReasonField)
	}
}

func testViewModelMapper(t *testing.T, location *time.Location, now time.Time) Mapper {
	t.Helper()
	publicURL, err := publicurl.Parse("http://example.test/base")
	if err != nil {
		t.Fatal(err)
	}
	mapper := NewMapper(publicURL, location, true)
	mapper.Now = func() time.Time { return now }
	return mapper
}
