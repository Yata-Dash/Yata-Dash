package fetch

import (
	"os"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

// TestMain relaxes DefBaseURLPolicy for the suite, because httptest servers
// bind to 127.0.0.1 and several fixtures point a def's api.base_url at one.
//
// This is deliberately an explicit opt-in in test code rather than a permissive
// production default. The previous arrangement had it the other way round — the
// shipped policy allowed private addresses so that the tests would pass — which
// let a test-suite convenience decide what every user's install permits. If
// this line is ever deleted, the tests break and nobody's security changes;
// under the old arrangement the failure mode was silent and pointed the other
// way.
func TestMain(m *testing.M) {
	DefBaseURLPolicy = netguard.Policy{AllowPrivate: true}
	os.Exit(m.Run())
}
