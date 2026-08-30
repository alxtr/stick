package core

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validation limits define the maximum accepted input lengths.
const (
	MaxStickNameLength   = 100
	MaxClaimReasonLength = 500
)

// ValidateStickName checks that name is non-empty, within its length limit,
// and contains only letters, digits, hyphens, and spaces.
func ValidateStickName(name string) error {
	if !isValidBoundedText(name, MaxStickNameLength) {
		return ErrInvalidStickName
	}
	for _, ch := range name {
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) && ch != '-' && ch != ' ' {
			return ErrInvalidStickName
		}
	}
	return nil
}

// ValidateClaimReason checks that reason is non-empty and within its length limit.
func ValidateClaimReason(reason string) error {
	if !isValidBoundedText(reason, MaxClaimReasonLength) {
		return ErrInvalidClaimReason
	}
	return nil
}

func isValidBoundedText(value string, maxLength int) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= maxLength
}
