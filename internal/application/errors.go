package application

import "errors"

// Application errors describe persistence and authorization outcomes.
var (
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("stick not found")
	ErrAlreadyExists   = errors.New("stick already exists")
	ErrVersionConflict = errors.New("stick version conflict")
)
