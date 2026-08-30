package core

import "time"

// Identity identifies an authenticated user and their authorization state.
type Identity struct {
	Sub           string
	Name          string
	Email         string
	EmailVerified bool
	IsAdmin       bool
}

// Holder identifies who currently holds a stick.
type Holder struct {
	Sub       string
	Name      string
	Email     string
	ClaimedAt time.Time
	Reason    string
}

// Stick is a stick's current state.
type Stick struct {
	ID         string
	Name       string
	Version    int64
	ArchivedAt *time.Time
	Holder     *Holder
}

// Available reports whether the stick has no current holder.
func (s Stick) Available() bool { return s.Holder == nil }

// Archived reports whether the stick has been archived.
func (s Stick) Archived() bool { return s.ArchivedAt != nil }

// Session represents one claim/release cycle.
type Session struct {
	ID          int64
	StickID     string
	HolderSub   string
	HolderName  string
	HolderEmail string
	Reason      string
	ClaimedAt   time.Time
	ReleasedAt  *time.Time
}
