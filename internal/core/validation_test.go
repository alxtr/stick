package core_test

import (
	"errors"
	"strings"
	"testing"

	domain "stick/internal/core"
)

func TestValidateStickName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "letters digits hyphens and spaces", value: "Deploy Key-2", valid: true},
		{name: "unicode letters", value: "Clé 2", valid: true},
		{name: "maximum length", value: strings.Repeat("a", domain.MaxStickNameLength), valid: true},
		{name: "empty", value: "", valid: false},
		{name: "whitespace", value: "   ", valid: false},
		{name: "too long", value: strings.Repeat("a", domain.MaxStickNameLength+1), valid: false},
		{name: "punctuation", value: "Deploy_Key", valid: false},
		{name: "newline", value: "Deploy\nKey", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := domain.ValidateStickName(test.value)
			if test.valid && err != nil {
				t.Fatalf("ValidateStickName(%q) = %v", test.value, err)
			}
			if !test.valid && !errors.Is(err, domain.ErrInvalidStickName) {
				t.Fatalf("ValidateStickName(%q) = %v, want %v", test.value, err, domain.ErrInvalidStickName)
			}
		})
	}
}

func TestValidateClaimReason(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "text", value: "Production deployment", valid: true},
		{name: "maximum length", value: strings.Repeat("a", domain.MaxClaimReasonLength), valid: true},
		{name: "empty", value: "", valid: false},
		{name: "whitespace", value: " \t\n", valid: false},
		{name: "too long", value: strings.Repeat("a", domain.MaxClaimReasonLength+1), valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := domain.ValidateClaimReason(test.value)
			if test.valid && err != nil {
				t.Fatalf("ValidateClaimReason(%q) = %v", test.value, err)
			}
			if !test.valid && !errors.Is(err, domain.ErrInvalidClaimReason) {
				t.Fatalf("ValidateClaimReason(%q) = %v, want %v", test.value, err, domain.ErrInvalidClaimReason)
			}
		})
	}
}

func TestValidationErrorMessages(t *testing.T) {
	if got := domain.ErrInvalidStickName.Error(); got != "invalid stick name" {
		t.Fatalf("ErrInvalidStickName = %q", got)
	}
	if got := domain.ErrInvalidClaimReason.Error(); got != "invalid claim reason" {
		t.Fatalf("ErrInvalidClaimReason = %q", got)
	}
}
