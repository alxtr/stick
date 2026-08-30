package postgres

import (
	"os"
	"testing"

	"stick/test/testsupport/postgrestest"
)

func TestMain(m *testing.M) {
	os.Exit(postgrestest.Run(m))
}
