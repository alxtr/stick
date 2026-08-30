// Package views maps application values to the data rendered by Web UI
// templates. It deliberately has no dependency on a route package.
package views

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	domain "stick/internal/core"
	"stick/internal/format"
	"stick/internal/publicurl"
	"stick/internal/web/httpx"
	"stick/internal/web/security"
)

// CurrentUserViewModel contains the user details displayed by a page.
type CurrentUserViewModel struct {
	Email          string
	ShowAdminBadge bool
}

// PageViewModel contains data shared by all page templates.
type PageViewModel struct {
	CurrentUser   CurrentUserViewModel
	CSRFToken     string
	StylesheetURL string
	LogoutAction  string
	HomeURL       string
	ShowTopbar    bool
}

// TextFieldViewModel contains the values and validation state for a text field.
type TextFieldViewModel struct {
	Value        string
	DescribedBy  string
	ErrorID      string
	ErrorMessage string
	HasError     bool
}

// DashboardViewModel contains the data rendered by the dashboard page.
type DashboardViewModel struct {
	PageViewModel
	Sticks             []StickCardViewModel
	CanCreateStick     bool
	NewStickURL        string
	ShowArchivedSticks bool
	ArchivedSticks     []ArchivedStickViewModel
}

// StickActionFormViewModel contains the data needed to render a stick action form.
type StickActionFormViewModel struct {
	Action      string
	CSRFToken   string
	FormVersion int64
	RedirectTo  string
	ButtonClass string
	ButtonLabel string
}

// StickCardViewModel contains the data rendered for a stick on the dashboard.
type StickCardViewModel struct {
	Name            string
	DetailURL       string
	DetailAriaLabel string
	HolderEmail     string
	Reason          string
	HeldDuration    string
	HeldByMe        bool
	HeldByOther     bool
	CanClaim        bool
	ActionForm      *StickActionFormViewModel
}

// ArchivedStickViewModel contains the data rendered for an archived stick.
type ArchivedStickViewModel struct {
	Name        string
	DetailURL   string
	RestoreForm *StickActionFormViewModel
}

// DetailViewModel contains the data rendered by the stick detail page.
type DetailViewModel struct {
	PageViewModel
	Stick              StickDetailViewModel
	Sessions           []HistorySessionViewModel
	PageLinks          []PaginationLinkViewModel
	TotalSessionsLabel string
	Error              string
}

// DetailFormState contains submitted values and validation errors for the detail page.
type DetailFormState struct {
	Alert            string
	RenameValue      *string
	RenameError      string
	ClaimReasonValue string
	ClaimReasonError string
}

// StickDetailViewModel contains the data rendered for a stick's details.
type StickDetailViewModel struct {
	Name              string
	DisplayID         string
	FormVersion       int64
	Icon              string
	StatusLabel       string
	StatusClass       string
	HolderEmail       string
	Reason            string
	HeldSince         string
	HeldDuration      string
	IsArchived        bool
	HeldByMe          bool
	HeldByOther       bool
	CanClaim          bool
	ShowAdminControls bool
	CanRename         bool
	RenameAction      string
	ClaimAction       string
	AdminActionForm   *StickActionFormViewModel
	StateActionForm   *StickActionFormViewModel
	RenameField       TextFieldViewModel
	ClaimReasonField  TextFieldViewModel
}

// HistorySessionViewModel contains the data rendered for one history session.
type HistorySessionViewModel struct {
	Reason      string
	HolderEmail string
	Duration    string
	TimeRange   string
}

// PaginationLinkViewModel contains the data rendered for one pagination link.
type PaginationLinkViewModel struct {
	Number    int
	URL       string
	IsCurrent bool
}

// NewStickViewModel contains the data rendered by the new-stick page.
type NewStickViewModel struct {
	PageViewModel
	CreateAction string
	Error        string
	NameField    TextFieldViewModel
}

// NewStickFormState contains submitted values and validation errors for the new-stick page.
type NewStickFormState struct {
	Name      string
	NameError string
	Alert     string
}

// ErrorPageViewModel contains the data rendered by an error page.
type ErrorPageViewModel struct {
	PageViewModel
	Title     string
	Icon      string
	Heading   string
	Message   string
	RequestID string
}

// Mapper is the boundary between application/domain values and data that
// templates may render. Its action flags control presentation only; the
// application service remains responsible for authorizing every mutation.
type Mapper struct {
	PublicURL            publicurl.URL
	Location             *time.Location
	NotificationsEnabled bool
	// Now is injectable for deterministic presentation tests. A nil value uses
	// time.Now.
	Now func() time.Time
}

// NewMapper returns a view mapper configured for an application mount.
func NewMapper(publicURL publicurl.URL, location *time.Location, notificationsEnabled bool) Mapper {
	if location == nil {
		location = time.UTC
	}
	return Mapper{
		PublicURL:            publicURL,
		Location:             location,
		NotificationsEnabled: notificationsEnabled,
		Now:                  time.Now,
	}
}

func (m Mapper) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// Dashboard maps application values to the dashboard view model.
func (m Mapper) Dashboard(
	r *http.Request,
	identity domain.Identity,
	sticks []domain.Stick,
	subscribedStickIDs []string,
	archivedSticks []domain.Stick,
) DashboardViewModel {
	pageView := m.page(r, identity)
	subscribed := stringSet(subscribedStickIDs)
	now := m.now()
	stickViews := make([]StickCardViewModel, 0, len(sticks))
	for _, stick := range sticks {
		stickViews = append(stickViews, m.stickCard(
			stick,
			identity,
			subscribed[stick.ID],
			now,
			pageView.CSRFToken,
			pageView.HomeURL,
		))
	}

	archivedViews := make([]ArchivedStickViewModel, 0, len(archivedSticks))
	for _, stick := range archivedSticks {
		canRestore := identity.IsAdmin && stick.Archived()
		archivedView := ArchivedStickViewModel{
			Name:      stick.Name,
			DetailURL: m.stickURL(stick.ID, ""),
		}
		if canRestore {
			archivedView.RestoreForm = newStickActionForm(
				m.stickURL(stick.ID, "/unarchive"),
				pageView.CSRFToken,
				stick.Version,
				"",
				"",
				"Restore",
			)
		}
		archivedViews = append(archivedViews, archivedView)
	}

	return DashboardViewModel{
		PageViewModel:      pageView,
		Sticks:             stickViews,
		CanCreateStick:     identity.IsAdmin,
		NewStickURL:        httpx.Path(m.PublicURL, "/sticks/new"),
		ShowArchivedSticks: identity.IsAdmin && len(archivedViews) > 0,
		ArchivedSticks:     archivedViews,
	}
}

func (m Mapper) stickCard(
	stick domain.Stick,
	identity domain.Identity,
	isSubscribed bool,
	now time.Time,
	csrfToken string,
	redirectTo string,
) StickCardViewModel {
	isArchived := stick.Archived()
	isAvailable := stick.Available()
	heldByMe := !isArchived && !isAvailable && stick.Holder.Sub == identity.Sub
	heldByOther := !isArchived && !isAvailable && !heldByMe
	isSubscribed = heldByOther && isSubscribed

	view := StickCardViewModel{
		Name:            stick.Name,
		DetailURL:       m.stickURL(stick.ID, ""),
		DetailAriaLabel: "View " + stick.Name + " details",
		HeldByMe:        heldByMe,
		HeldByOther:     heldByOther,
		CanClaim:        !isArchived && isAvailable,
	}
	if stick.Holder != nil {
		view.HolderEmail = stick.Holder.Email
		view.Reason = stick.Holder.Reason
		view.HeldDuration = m.elapsed(stick.Holder.ClaimedAt, now)
	}
	switch {
	case heldByMe:
		view.ActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/release"), csrfToken, stick.Version, redirectTo, "btn btn-release", "Put down",
		)
	case m.NotificationsEnabled && heldByOther && isSubscribed:
		view.ActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/notify/cancel"), csrfToken, stick.Version, redirectTo, "btn btn-notify-cancel", "Cancel notification",
		)
	case m.NotificationsEnabled && heldByOther:
		view.ActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/notify"), csrfToken, stick.Version, redirectTo, "btn btn-notify", "Notify me",
		)
	}
	return view
}

// Detail maps application values to the stick detail view model.
func (m Mapper) Detail(
	r *http.Request,
	identity domain.Identity,
	stick domain.Stick,
	sessions []domain.Session,
	page, totalPages, totalSessions int,
	pageNumbers []int,
	subscribedStickIDs []string,
	form DetailFormState,
) DetailViewModel {
	pageView := m.page(r, identity)
	subscribed := stringSet(subscribedStickIDs)[stick.ID]
	stickView := m.stickDetail(stick, identity, subscribed, m.now(), pageView.CSRFToken, form)

	sessionViews := make([]HistorySessionViewModel, 0, len(sessions))
	for _, session := range sessions {
		sessionViews = append(sessionViews, m.historySession(session))
	}

	var pageLinks []PaginationLinkViewModel
	if totalPages > 1 {
		pageLinks = make([]PaginationLinkViewModel, 0, len(pageNumbers))
		for _, link := range pageNumbers {
			pageLinks = append(pageLinks, PaginationLinkViewModel{
				Number:    link,
				URL:       m.stickURL(stick.ID, "?page="+strconv.Itoa(link)),
				IsCurrent: link == page,
			})
		}
	}

	return DetailViewModel{
		PageViewModel:      pageView,
		Stick:              stickView,
		Sessions:           sessionViews,
		PageLinks:          pageLinks,
		TotalSessionsLabel: fmt.Sprintf("%d sessions total", totalSessions),
		Error:              form.Alert,
	}
}

func (m Mapper) stickDetail(
	stick domain.Stick,
	identity domain.Identity,
	isSubscribed bool,
	now time.Time,
	csrfToken string,
	form DetailFormState,
) StickDetailViewModel {
	isArchived := stick.Archived()
	isAvailable := stick.Available()
	heldByMe := !isArchived && !isAvailable && stick.Holder.Sub == identity.Sub
	heldByOther := !isArchived && !isAvailable && !heldByMe
	isSubscribed = heldByOther && isSubscribed
	canRename := identity.IsAdmin
	canArchive := identity.IsAdmin && !isArchived && isAvailable
	canRestore := identity.IsAdmin && isArchived
	renameValue := stick.Name
	if form.RenameValue != nil {
		renameValue = *form.RenameValue
	}

	view := StickDetailViewModel{
		Name:              stick.Name,
		DisplayID:         stick.ID,
		FormVersion:       stick.Version,
		IsArchived:        isArchived,
		HeldByMe:          heldByMe,
		HeldByOther:       heldByOther,
		CanClaim:          !isArchived && isAvailable,
		ShowAdminControls: canRename || canArchive || canRestore,
		CanRename:         canRename,
		RenameAction:      m.stickURL(stick.ID, "/rename"),
		ClaimAction:       m.stickURL(stick.ID, "/claim"),
		RenameField:       newTextField(renameValue, "", "rename-name-error", form.RenameError),
		ClaimReasonField:  newTextField(form.ClaimReasonValue, "", "claim-reason-error", form.ClaimReasonError),
	}

	switch {
	case isArchived:
		view.Icon = "📦"
		view.StatusLabel = "archived"
		view.StatusClass = "archived"
	case isAvailable:
		view.Icon = "🌳"
		view.StatusLabel = "available"
		view.StatusClass = "available"
	case heldByMe:
		view.Icon = "✋"
		view.StatusLabel = "yours"
		view.StatusClass = "mine"
	default:
		view.Icon = "🪓"
		view.StatusLabel = "in use"
		view.StatusClass = "taken"
	}

	if stick.Holder != nil {
		view.HolderEmail = stick.Holder.Email
		view.Reason = stick.Holder.Reason
		view.HeldSince = m.formatTime(stick.Holder.ClaimedAt)
		view.HeldDuration = m.elapsed(stick.Holder.ClaimedAt, now)
	}
	switch {
	case canRestore:
		view.AdminActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/unarchive"), csrfToken, stick.Version, "", "", "Restore stick",
		)
	case canArchive:
		view.AdminActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/archive"), csrfToken, stick.Version, "", "danger", "Archive stick",
		)
	}
	switch {
	case heldByMe:
		view.StateActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/release"), csrfToken, stick.Version, "", "btn-release", "Put down",
		)
	case m.NotificationsEnabled && heldByOther && isSubscribed:
		view.StateActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/notify/cancel"), csrfToken, stick.Version, "", "btn-notify-cancel", "Cancel notification",
		)
	case m.NotificationsEnabled && heldByOther:
		view.StateActionForm = newStickActionForm(
			m.stickURL(stick.ID, "/notify"), csrfToken, stick.Version, "", "btn-notify", "Notify me",
		)
	}
	return view
}

// StickDetail maps one stick's state and action controls.
func (m Mapper) StickDetail(
	stick domain.Stick,
	identity domain.Identity,
	isSubscribed bool,
	now time.Time,
	csrfToken string,
	form DetailFormState,
) StickDetailViewModel {
	return m.stickDetail(stick, identity, isSubscribed, now, csrfToken, form)
}

func (m Mapper) historySession(session domain.Session) HistorySessionViewModel {
	claimedAt := m.formatTime(session.ClaimedAt)
	releasedAt := ""
	view := HistorySessionViewModel{
		Reason:      session.Reason,
		HolderEmail: session.HolderEmail,
	}
	if session.ReleasedAt != nil {
		view.Duration = format.Duration(session.ReleasedAt.Sub(session.ClaimedAt).Round(time.Minute))
		releasedAt = m.formatTime(*session.ReleasedAt)
	}
	view.TimeRange = claimedAt + " → " + releasedAt
	return view
}

// NewStick maps form state to the new-stick view model.
func (m Mapper) NewStick(r *http.Request, identity domain.Identity, form NewStickFormState) NewStickViewModel {
	return NewStickViewModel{
		PageViewModel: m.page(r, identity),
		CreateAction:  httpx.Path(m.PublicURL, "/sticks/new"),
		Error:         form.Alert,
		NameField:     newTextField(form.Name, "name-hint", "name-error", form.NameError),
	}
}

// ErrorPage maps an HTTP status to the error page view model.
func (m Mapper) ErrorPage(r *http.Request, identity domain.Identity, status int) ErrorPageViewModel {
	view := ErrorPageViewModel{
		PageViewModel: m.page(r, identity),
		Title:         fmt.Sprintf("%d · stick", status),
		Icon:          "🍂",
	}
	switch status {
	case http.StatusBadRequest:
		view.Heading = "Invalid request"
		view.Message = "The form submission was invalid or too large. Return to the page and try again."
	case http.StatusForbidden:
		view.Heading = "Not allowed"
		view.Message = "You do not have permission to perform this action."
	case http.StatusNotFound:
		view.Heading = "Not found"
		view.Message = "The page or stick you requested could not be found."
	default:
		view.Heading = "Something went wrong"
		view.Message = "We could not complete your request. Try again later."
		if status >= http.StatusInternalServerError {
			view.RequestID = httpx.RequestID(r.Context())
		}
	}
	return view
}

func (m Mapper) page(r *http.Request, identity domain.Identity) PageViewModel {
	return PageViewModel{
		CurrentUser: CurrentUserViewModel{
			Email:          identity.Email,
			ShowAdminBadge: identity.IsAdmin,
		},
		CSRFToken:     security.CSRFToken(r),
		StylesheetURL: httpx.Path(m.PublicURL, "/assets/styles.css"),
		LogoutAction:  httpx.Path(m.PublicURL, "/auth/logout"),
		HomeURL:       httpx.Path(m.PublicURL, "/"),
		ShowTopbar:    identity.Sub != "",
	}
}

func (m Mapper) formatTime(value time.Time) string {
	location := m.Location
	if location == nil {
		location = time.UTC
	}
	return value.In(location).Format("Jan 2 · 15:04")
}

func (m Mapper) elapsed(since, now time.Time) string {
	duration := now.Sub(since).Round(time.Minute)
	if duration < time.Minute {
		return "just now"
	}
	return format.Duration(duration)
}

func (m Mapper) stickURL(id, suffix string) string {
	return httpx.Path(m.PublicURL, "/sticks/"+url.PathEscape(id)+suffix)
}

func newStickActionForm(
	action, csrfToken string, formVersion int64, redirectTo, buttonClass, buttonLabel string,
) *StickActionFormViewModel {
	return &StickActionFormViewModel{
		Action:      action,
		CSRFToken:   csrfToken,
		FormVersion: formVersion,
		RedirectTo:  redirectTo,
		ButtonClass: buttonClass,
		ButtonLabel: buttonLabel,
	}
}

func newTextField(value, hintID, errorID, errorMessage string) TextFieldViewModel {
	describedBy := hintID
	if errorMessage != "" {
		if describedBy != "" {
			describedBy += " "
		}
		describedBy += errorID
	}
	return TextFieldViewModel{
		Value:        value,
		DescribedBy:  describedBy,
		ErrorID:      errorID,
		ErrorMessage: errorMessage,
		HasError:     errorMessage != "",
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
