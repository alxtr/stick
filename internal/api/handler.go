// Package api provides the JSON REST API for Stick.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"stick/internal/application"
	"stick/internal/auth"
	domain "stick/internal/core"
	"stick/internal/publicurl"
	"stick/internal/web/httpx"
)

const (
	apiPrefix           = "/api/v1"
	maxJSONBodyBytes    = 1 << 20
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
	maxHistoryOffset    = 100_000
)

// Handler serves the Stick REST API.
type Handler struct {
	service              *application.Service
	validator            TokenValidator
	admins               map[string]struct{}
	publicURL            publicurl.URL
	notificationsEnabled bool
}

// TokenValidator authenticates one external bearer token.
type TokenValidator interface {
	Validate(context.Context, string) (domain.Identity, error)
}

// New returns an API handler.
func New(
	service *application.Service,
	validator TokenValidator,
	adminEmails []string,
	publicURL publicurl.URL,
	notificationsEnabled bool,
) *Handler {
	return &Handler{
		service:              service,
		validator:            validator,
		admins:               auth.AdminSet(adminEmails),
		publicURL:            publicURL,
		notificationsEnabled: notificationsEnabled,
	}
}

// Register adds all API routes to router. Authentication is applied to the
// complete API route family; health probes remain unauthenticated.
func Register(router *httpx.Router, handler *Handler) {
	protected := router.With(handler.authenticate)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks", handler.listSticks)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks", handler.createStick)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks/archived", handler.listArchivedSticks)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks/{id}", handler.getStick)
	protected.HandleFunc(http.MethodPatch, apiPrefix+"/sticks/{id}", handler.renameStick)
	protected.HandleFunc(http.MethodGet, apiPrefix+"/sticks/{id}/history", handler.history)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/claim", handler.claimStick)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/release", handler.releaseStick)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/archive", handler.archiveStick)
	protected.HandleFunc(http.MethodPost, apiPrefix+"/sticks/{id}/unarchive", handler.unarchiveStick)
	if handler.notificationsEnabled {
		protected.HandleFunc(http.MethodPut, apiPrefix+"/sticks/{id}/subscription", handler.subscribe)
		protected.HandleFunc(http.MethodDelete, apiPrefix+"/sticks/{id}/subscription", handler.unsubscribe)
		protected.HandleFunc(http.MethodGet, apiPrefix+"/subscriptions", handler.subscriptions)
	}
}

// NotFound writes the JSON response used for unknown API routes.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w)
			return
		}
		if h.validator == nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		identity, err := h.validator.Validate(r.Context(), parts[1])
		if err != nil {
			unauthorized(w)
			return
		}
		identity = auth.WithAdminStatus(identity, h.admins)
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	})
}

func (h *Handler) listSticks(w http.ResponseWriter, r *http.Request) {
	sticks, err := h.service.ListSticks(r.Context())
	if err != nil {
		internalError(w, r, "list sticks", err)
		return
	}
	writeCollection(w, r, sticksToJSON(sticks))
}

func (h *Handler) listArchivedSticks(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	if !identity.IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	sticks, err := h.service.ListArchivedSticks(r.Context(), identity)
	if err != nil {
		handleError(w, r, "list archived sticks", err)
		return
	}
	writeCollection(w, r, sticksToJSON(sticks))
}

func (h *Handler) getStick(w http.ResponseWriter, r *http.Request) {
	stick, err := h.service.GetStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		handleError(w, r, "get stick", err)
		return
	}
	setETag(w, stick.Version)
	if ifNoneMatch(headerValue(r, "If-None-Match"), etag(stick.Version)) {
		notModified(w)
		return
	}
	writeJSON(w, http.StatusOK, stickToJSON(stick))
}

func (h *Handler) createStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	stick, err := h.service.CreateStick(r.Context(), auth.IdentityFromContext(r.Context()), request.Name)
	if err != nil {
		handleError(w, r, "create stick", err)
		return
	}
	setETag(w, stick.Version)
	w.Header().Set("Location", httpx.Absolute(h.publicURL, apiPrefix+"/sticks/"+stick.ID))
	writeJSON(w, http.StatusCreated, stickToJSON(stick))
}

func (h *Handler) renameStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.RenameStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), request.Name, version)
	if err != nil {
		handleError(w, r, "rename stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) archiveStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.ArchiveStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version)
	if err != nil {
		handleError(w, r, "archive stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) unarchiveStick(w http.ResponseWriter, r *http.Request) {
	if !auth.IdentityFromContext(r.Context()).IsAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.UnarchiveStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version)
	if err != nil {
		handleError(w, r, "unarchive stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) claimStick(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.ClaimStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), request.Reason, version)
	if err != nil {
		handleError(w, r, "claim stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) releaseStick(w http.ResponseWriter, r *http.Request) {
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	stick, err := h.service.ReleaseStick(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version)
	if err != nil {
		handleError(w, r, "release stick", err)
		return
	}
	writeStick(w, stick)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	id := r.PathValue("id")
	stick, err := h.service.GetStick(r.Context(), identity, id)
	if err != nil {
		handleError(w, r, "get stick history", err)
		return
	}
	setETag(w, stick.Version)
	limit, offset, ok := historyPagination(w, r)
	if !ok {
		return
	}
	sessions, total, err := h.service.GetHistory(r.Context(), identity, id, limit, offset)
	if err != nil {
		handleError(w, r, "get stick history", err)
		return
	}
	response := struct {
		Sessions []sessionJSON `json:"sessions"`
		Total    int           `json:"total"`
		Limit    int           `json:"limit"`
		Offset   int           `json:"offset"`
	}{
		Sessions: sessionsToJSON(sessions),
		Total:    total,
		Limit:    limit,
		Offset:   offset,
	}
	writeConditionalJSON(w, r, response)
}

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := h.service.Subscribe(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version); err != nil {
		handleError(w, r, "subscribe to stick", err)
		return
	}
	setETag(w, version)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	version, ok := h.expectedVersion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := h.service.Unsubscribe(r.Context(), auth.IdentityFromContext(r.Context()), r.PathValue("id"), version); err != nil {
		handleError(w, r, "unsubscribe from stick", err)
		return
	}
	setETag(w, version)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) subscriptions(w http.ResponseWriter, r *http.Request) {
	ids, err := h.service.SubscribedStickIDs(r.Context(), auth.IdentityFromContext(r.Context()))
	if err != nil {
		internalError(w, r, "list subscriptions", err)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	writeCollection(w, r, ids)
}

func (h *Handler) expectedVersion(w http.ResponseWriter, r *http.Request, id string) (int64, bool) {
	header := headerValue(r, "If-Match")
	if strings.TrimSpace(header) == "" {
		writeError(w, http.StatusPreconditionRequired, "If-Match header is required")
		return 0, false
	}
	tags, wildcard, err := parseIfMatch(header)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid If-Match header")
		return 0, false
	}
	if len(tags) == 1 && !wildcard {
		return tags[0], true
	}

	// A wildcard or a list of tags must be compared to the current
	// representation before choosing the version passed to the service.
	stick, err := h.service.GetStick(r.Context(), auth.IdentityFromContext(r.Context()), id)
	if err != nil {
		handleError(w, r, "get stick", err)
		return 0, false
	}
	if !wildcard {
		matched := false
		for _, tag := range tags {
			if tag == stick.Version {
				matched = true
				break
			}
		}
		if !matched {
			writeError(w, http.StatusPreconditionFailed, "precondition failed")
			return 0, false
		}
	}
	return stick.Version, true
}

func historyPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	query := r.URL.Query()
	limit := defaultHistoryLimit
	offset := 0
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxHistoryLimit {
			writeError(w, http.StatusBadRequest, "invalid history limit")
			return 0, 0, false
		}
	}
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > maxHistoryOffset {
			writeError(w, http.StatusBadRequest, "invalid history offset")
			return 0, 0, false
		}
	}
	return limit, offset, true
}

func parseIfMatch(value string) ([]int64, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, errors.New("missing If-Match")
	}
	if value == "*" {
		return nil, true, nil
	}
	parts := strings.Split(value, ",")
	tags := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 3 || part[0] != '"' || part[len(part)-1] != '"' || strings.HasPrefix(part, "W/") {
			return nil, false, errors.New("invalid entity tag")
		}
		version, err := strconv.ParseInt(part[1:len(part)-1], 10, 64)
		if err != nil || version < 1 || etag(version) != part {
			return nil, false, errors.New("invalid entity tag")
		}
		tags = append(tags, version)
	}
	if len(tags) == 0 {
		return nil, false, errors.New("missing entity tag")
	}
	return tags, false, nil
}

func ifNoneMatch(header, current string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			return true
		}
		if strings.HasPrefix(part, "W/") {
			part = strings.TrimSpace(strings.TrimPrefix(part, "W/"))
		}
		if part == current {
			return true
		}
	}
	return false
}

func writeStick(w http.ResponseWriter, stick domain.Stick) {
	setETag(w, stick.Version)
	writeJSON(w, http.StatusOK, stickToJSON(stick))
}

func writeCollection(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	sum := sha256.Sum256(data)
	collectionETag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", collectionETag)
	if ifNoneMatch(headerValue(r, "If-None-Match"), collectionETag) {
		notModified(w)
		return
	}
	writeData(w, http.StatusOK, data)
}

func writeConditionalJSON(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	sum := sha256.Sum256(data)
	representationETag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", representationETag)
	if ifNoneMatch(headerValue(r, "If-None-Match"), representationETag) {
		notModified(w)
		return
	}
	writeData(w, http.StatusOK, data)
}

func headerValue(r *http.Request, name string) string {
	return strings.Join(r.Header.Values(name), ",")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeData(w, status, data)
}

func writeData(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func notModified(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotModified)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func setETag(w http.ResponseWriter, version int64) { w.Header().Set("ETag", etag(version)) }

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

func internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	httpx.LogError(r.Context(), "request failed", operation, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func handleError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case errors.Is(err, application.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, application.ErrForbidden), errors.Is(err, domain.ErrNotHolder):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, application.ErrVersionConflict):
		writeError(w, http.StatusPreconditionFailed, "precondition failed")
	case errors.Is(err, application.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, domain.ErrInvalidStickName), errors.Is(err, domain.ErrInvalidClaimReason):
		writeError(w, http.StatusUnprocessableEntity, "invalid request")
	case errors.Is(err, domain.ErrAlreadyHeld), errors.Is(err, domain.ErrAlreadyArchived),
		errors.Is(err, domain.ErrNotArchived), errors.Is(err, domain.ErrHeld),
		errors.Is(err, domain.ErrVersionExhausted):
		writeError(w, http.StatusConflict, "conflict")
	default:
		internalError(w, r, operation, err)
	}
}

type stickJSON struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Version    int64       `json:"version"`
	Archived   bool        `json:"archived"`
	Available  bool        `json:"available"`
	ArchivedAt *time.Time  `json:"archived_at"`
	Holder     *holderJSON `json:"holder"`
}

type holderJSON struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	ClaimedAt string `json:"claimed_at"`
	Reason    string `json:"reason"`
}

type sessionJSON struct {
	ID          int64   `json:"id"`
	StickID     string  `json:"stick_id"`
	HolderName  string  `json:"holder_name"`
	HolderEmail string  `json:"holder_email"`
	Reason      string  `json:"reason"`
	ClaimedAt   string  `json:"claimed_at"`
	ReleasedAt  *string `json:"released_at"`
}

func stickToJSON(stick domain.Stick) stickJSON {
	response := stickJSON{
		ID:         stick.ID,
		Name:       stick.Name,
		Version:    stick.Version,
		Archived:   stick.Archived(),
		Available:  stick.Available(),
		ArchivedAt: stick.ArchivedAt,
	}
	if stick.Holder != nil {
		response.Holder = &holderJSON{
			Name:      stick.Holder.Name,
			Email:     stick.Holder.Email,
			ClaimedAt: stick.Holder.ClaimedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			Reason:    stick.Holder.Reason,
		}
	}
	return response
}

func sticksToJSON(sticks []domain.Stick) []stickJSON {
	result := make([]stickJSON, 0, len(sticks))
	for _, stick := range sticks {
		result = append(result, stickToJSON(stick))
	}
	return result
}

func sessionsToJSON(sessions []domain.Session) []sessionJSON {
	result := make([]sessionJSON, 0, len(sessions))
	for _, session := range sessions {
		item := sessionJSON{
			ID:          session.ID,
			StickID:     session.StickID,
			HolderName:  session.HolderName,
			HolderEmail: session.HolderEmail,
			Reason:      session.Reason,
			ClaimedAt:   session.ClaimedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
		if session.ReleasedAt != nil {
			releasedAt := session.ReleasedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
			item.ReleasedAt = &releasedAt
		}
		result = append(result, item)
	}
	return result
}
