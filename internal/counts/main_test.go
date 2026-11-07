package counts

import (
	"os"
	"testing"
)

// TestMain clears the machine's repo-discovery env so tests see only the repos
// they create. A test that wants extra repos sets BD_COUNTS_EXTRA_REPOS itself.
func TestMain(m *testing.M) {
	for _, k := range []string{"BD_COUNTS_EXTRA_REPOS", "BD_COUNTS_PROJECTS_DIR", "BD_COUNTS_CACHE_DIR", "WRAP_COUNTS_FILE"} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
