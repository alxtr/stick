package mongodb

import (
	"context"
	"strings"
	"testing"
)

func TestOpenDoesNotExposeDatabaseURL(t *testing.T) {
	const databaseURL = "mongodb://user:super-secret-password@%gh/database"
	_, err := Open(context.Background(), databaseURL)
	if err == nil {
		t.Fatal("invalid database URL unexpectedly opened")
	}
	if strings.Contains(err.Error(), databaseURL) || strings.Contains(err.Error(), "super-secret-password") {
		t.Fatalf("Open exposed database URL credentials: %v", err)
	}
}

func TestDatabaseNameFromURL(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
		want string
	}{
		{name: "standard", url: "mongodb://localhost/stick", want: "stick"},
		{name: "srv", url: "mongodb+srv://cluster.example/stick", want: "stick"},
		{name: "escaped", url: "mongodb://localhost/stick%2Dprod", want: "stick-prod"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := databaseNameFromURL(test.url)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("database name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDatabaseNameFromURLRejectsInvalidURLs(t *testing.T) {
	for _, databaseURL := range []string{
		"",
		"postgres://localhost/stick",
		"mongodb:///stick",
		"mongodb://localhost",
		"mongodb://localhost/stick/extra",
		"mongodb://localhost/stick%2Fextra",
		"mongodb://localhost/%zz",
	} {
		t.Run(databaseURL, func(t *testing.T) {
			if _, err := databaseNameFromURL(databaseURL); err == nil {
				t.Fatalf("databaseNameFromURL(%q) unexpectedly succeeded", databaseURL)
			}
		})
	}
}

func TestValidateMigrations(t *testing.T) {
	for _, test := range []struct {
		name       string
		migrations []migration
		wantError  bool
	}{
		{name: "valid", migrations: []migration{{version: 1, name: "001_baseline"}}},
		{name: "empty", wantError: true},
		{name: "gap", migrations: []migration{{version: 2, name: "002_future"}}, wantError: true},
		{name: "missing name", migrations: []migration{{version: 1}}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMigrations(test.migrations); (err != nil) != test.wantError {
				t.Fatalf("validateMigrations error = %v, want error=%t", err, test.wantError)
			}
		})
	}
}
