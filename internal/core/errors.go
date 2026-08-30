// Package core defines Stick's core entities and business rules.
package core

import "errors"

// Domain errors describe invalid or conflicting stick state.
var (
	ErrAlreadyHeld        = errors.New("stick is already held")
	ErrNotHolder          = errors.New("caller is not the current holder")
	ErrAlreadyArchived    = errors.New("stick is already archived")
	ErrNotArchived        = errors.New("stick is not archived")
	ErrHeld               = errors.New("stick must be available")
	ErrInvalidStickName   = errors.New("invalid stick name")
	ErrInvalidClaimReason = errors.New("invalid claim reason")
	ErrVersionExhausted   = errors.New("stick version exhausted")
)
