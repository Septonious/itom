package tunnel

import "testing"

// Pure boundary assertions share one scenario. Resource-owning and concurrent
// subtests retain t.Run so their cleanup, timeout and goroutine scope is unchanged.
func checkScenarioBoundary(t *testing.T, name string, check func(*testing.T)) bool {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("boundary=%s", name)
		}
	}()
	check(t)
	return !t.Failed()
}
