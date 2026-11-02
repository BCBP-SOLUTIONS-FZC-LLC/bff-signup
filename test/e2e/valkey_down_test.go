//go:build e2e

package e2e_test

// Stage 2: the "Valkey is down" cases SB1-03, SB2-41, H-03 and H-04, checked in
// ONE outage (Valkey is stopped once, all four checks run, then it is started
// again). Needs Docker, so it only runs when E2E_VALKEY_CONTAINER names the
// Valkey container (SB3-14 in gap_fixes_test.go works the same way).

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValkeyDown_SB1_03_SB2_41_H03_H04(t *testing.T) {
	container := os.Getenv("E2E_VALKEY_CONTAINER")
	if container == "" {
		t.Skip("set E2E_VALKEY_CONTAINER=<valkey container name> to run this test")
	}
	// A session made while Valkey is still up, used for the SB-2 check.
	s := newSession(t, randomIP(), nil)
	payload := s.trialPayload(nil)

	require.NoError(t, exec.CommandContext(t.Context(), "docker", "stop", container).Run())
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "start", container).Run()
		time.Sleep(3 * time.Second)
	})

	t.Run("SB1-03 session returns 503 (fail-closed)", func(t *testing.T) {
		r := getSession(t, randomIP())
		assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
		assert.Contains(t, r.text(), "DEPENDENCY_UNAVAILABLE")
	})

	t.Run("SB2-41 trial returns 503", func(t *testing.T) {
		r := s.postTrial(t, payload)
		assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
	})

	t.Run("H-03 readyz returns 503", func(t *testing.T) {
		r, err := rawDo(t.Context(), http.MethodGet, baseURL+"/readyz", "", nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, r.status, r.text())
	})

	t.Run("H-04 healthz stays 200", func(t *testing.T) {
		r, err := rawDo(t.Context(), http.MethodGet, baseURL+"/healthz", "", nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, r.status)
	})
}
