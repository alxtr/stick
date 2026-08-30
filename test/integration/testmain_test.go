// Package integration_test exercises cross-adapter integration behavior.
package integration_test

import (
	"os"
	"testing"

	"stick/test/testsupport/mongotest"
	"stick/test/testsupport/postgrestest"
)

func TestMain(m *testing.M) {
	os.Exit(postgrestest.RunWith(func() int {
		return mongotest.Run(m)
	}))
}
