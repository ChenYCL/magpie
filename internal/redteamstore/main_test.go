package redteamstore

import (
	"os"
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// The tests run in a home of their own: a store they make is never the
// user's, and the database a test leaves behind goes with the home.
func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
