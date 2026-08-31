package sticks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"stick/internal/api/requestlog"
	"stick/internal/application"
	domain "stick/internal/core"
)

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
	requestlog.LogError(r.Context(), "request failed", operation, err)
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
