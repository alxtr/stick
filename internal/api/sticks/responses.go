package sticks

import (
	"errors"
	"net/http"
	"time"

	"stick/internal/application"
	domain "stick/internal/core"
	"stick/internal/httpx"
)

func writeStick(w http.ResponseWriter, stick domain.Stick) {
	httpx.SetETag(w, stick.Version)
	httpx.WriteJSON(w, http.StatusOK, stickToJSON(stick))
}

func handleError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case errors.Is(err, application.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, application.ErrForbidden), errors.Is(err, domain.ErrNotHolder):
		httpx.WriteError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, application.ErrVersionConflict):
		httpx.WriteError(w, http.StatusPreconditionFailed, "precondition failed")
	case errors.Is(err, application.ErrAlreadyExists):
		httpx.WriteError(w, http.StatusConflict, "conflict")
	case errors.Is(err, domain.ErrInvalidStickName), errors.Is(err, domain.ErrInvalidClaimReason):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "invalid request")
	case errors.Is(err, domain.ErrAlreadyHeld), errors.Is(err, domain.ErrAlreadyArchived),
		errors.Is(err, domain.ErrNotArchived), errors.Is(err, domain.ErrHeld),
		errors.Is(err, domain.ErrVersionExhausted):
		httpx.WriteError(w, http.StatusConflict, "conflict")
	default:
		httpx.InternalError(w, r, operation, err)
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
