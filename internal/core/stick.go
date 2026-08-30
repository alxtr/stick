package core

import (
	"math"
	"time"
)

// NewStick returns the initial state of a stick. Versions start at one.
func NewStick(id, name string) (Stick, error) {
	if err := ValidateStickName(name); err != nil {
		return Stick{}, err
	}
	return Stick{ID: id, Name: name, Version: 1}, nil
}

// Rename returns the state produced by renaming stick.
func Rename(stick Stick, name string) (Stick, error) {
	if err := ValidateStickName(name); err != nil {
		return Stick{}, err
	}
	if err := incrementVersion(&stick); err != nil {
		return Stick{}, err
	}
	stick.Name = name
	return stick, nil
}

// Archive returns the state produced by archiving an available stick.
func Archive(stick Stick, at time.Time) (Stick, error) {
	if stick.Archived() {
		return Stick{}, ErrAlreadyArchived
	}
	if !stick.Available() {
		return Stick{}, ErrHeld
	}
	if err := incrementVersion(&stick); err != nil {
		return Stick{}, err
	}
	at = at.UTC()
	stick.ArchivedAt = &at
	return stick, nil
}

// Unarchive returns the state produced by restoring an archived stick.
func Unarchive(stick Stick) (Stick, error) {
	if !stick.Archived() {
		return Stick{}, ErrNotArchived
	}
	if err := incrementVersion(&stick); err != nil {
		return Stick{}, err
	}
	stick.ArchivedAt = nil
	return stick, nil
}

// Claim returns the state produced by identity claiming stick.
func Claim(stick Stick, identity Identity, reason string, at time.Time) (Stick, error) {
	if err := ValidateClaimReason(reason); err != nil {
		return Stick{}, err
	}
	if stick.Archived() {
		return Stick{}, ErrAlreadyArchived
	}
	if !stick.Available() {
		return Stick{}, ErrAlreadyHeld
	}
	if err := incrementVersion(&stick); err != nil {
		return Stick{}, err
	}
	stick.Holder = &Holder{
		Sub:       identity.Sub,
		Name:      identity.Name,
		Email:     identity.Email,
		ClaimedAt: at.UTC(),
		Reason:    reason,
	}
	return stick, nil
}

// Release returns the state produced by the current holder releasing stick.
func Release(stick Stick, subject string) (Stick, error) {
	if stick.Available() || stick.Holder.Sub != subject {
		return Stick{}, ErrNotHolder
	}
	if err := incrementVersion(&stick); err != nil {
		return Stick{}, err
	}
	stick.Holder = nil
	return stick, nil
}

func incrementVersion(stick *Stick) error {
	if stick.Version < 1 || stick.Version == math.MaxInt64 {
		return ErrVersionExhausted
	}
	stick.Version++
	return nil
}
