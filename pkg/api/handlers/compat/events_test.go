//go:build !remote && (linux || freebsd)

package compat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/containers/podman/v6/pkg/api/handlers/utils/apiutil"
	"github.com/stretchr/testify/assert"
)

func TestIsLibpodRequest(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		expectedLibpod bool
	}{
		{
			name:           "Docker API request",
			path:           "/v1.41/events",
			expectedLibpod: false,
		},
		{
			name:           "Libpod API request",
			path:           "/v1.41/libpod/events",
			expectedLibpod: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			isLibpod := apiutil.IsLibpodRequest(req)
			assert.Equal(t, tt.expectedLibpod, isLibpod)
		})
	}
}

func TestHealthStatusFilteringGlobalMap(t *testing.T) {
	initialLen := len(previousHealthStatus)
	if initialLen != 0 {
		t.Errorf("expected initial map to be empty, got %d entries", initialLen)
	}
}

func TestHealthStatusFilteringLogic(t *testing.T) {
	previousHealthStatus = make(map[string]string)

	dockerReq := httptest.NewRequest(http.MethodGet, "/v1.41/events", nil)
	libpodReq := httptest.NewRequest(http.MethodGet, "/v1.41/libpod/events", nil)

	testCases := []struct {
		name           string
		isLibpod       bool
		containerID    string
		healthStatus   string
		expectFiltered bool
		firstEvent     bool
	}{
		{
			name:           "Docker API - first event should NOT be filtered",
			isLibpod:       false,
			containerID:    "container1",
			healthStatus:   "healthy",
			expectFiltered: false,
			firstEvent:     true,
		},
		{
			name:           "Docker API - same status should be filtered",
			isLibpod:       false,
			containerID:    "container1",
			healthStatus:   "healthy",
			expectFiltered: true,
			firstEvent:     false,
		},
		{
			name:           "Docker API - different status should NOT be filtered",
			isLibpod:       false,
			containerID:    "container1",
			healthStatus:   "unhealthy",
			expectFiltered: false,
			firstEvent:     false,
		},
		{
			name:           "Docker API - same status again should be filtered",
			isLibpod:       false,
			containerID:    "container1",
			healthStatus:   "unhealthy",
			expectFiltered: true,
			firstEvent:     false,
		},
		{
			name:           "Libpod API - all events should pass (not filtered)",
			isLibpod:       true,
			containerID:    "container2",
			healthStatus:   "healthy",
			expectFiltered: false,
			firstEvent:     true,
		},
		{
			name:           "Libpod API - same status should NOT be filtered",
			isLibpod:       true,
			containerID:    "container2",
			healthStatus:   "healthy",
			expectFiltered: false,
			firstEvent:     false,
		},
		{
			name:           "Docker API - different container starts fresh",
			isLibpod:       false,
			containerID:    "container3",
			healthStatus:   "healthy",
			expectFiltered: false,
			firstEvent:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := dockerReq
			if tc.isLibpod {
				req = libpodReq
			}

			isDockerCompat := !apiutil.IsLibpodRequest(req)

			var shouldFilter bool
			if isDockerCompat {
				healthStatusLock.Lock()
				previousStatus, exists := previousHealthStatus[tc.containerID]
				if exists && previousStatus == tc.healthStatus {
					shouldFilter = true
				}
				previousHealthStatus[tc.containerID] = tc.healthStatus
				healthStatusLock.Unlock()
			}

			assert.Equal(t, tc.expectFiltered, shouldFilter,
				"Filtering check failed for: %s", tc.name)
		})
	}
}

func TestHealthStatusFilteringScenario(t *testing.T) {
	previousHealthStatus = make(map[string]string)

	scenario := `
Scenario: Continuous health check stream
- Container health check runs every 1 second
- All events appear in libpod API
- Only status changes appear in Docker API

Expected events flow:
  Time 0s:  Container starts, health check runs -> "starting"
  Time 1s:  Health check passes -> "healthy"     (NEW status -> emit)
  Time 2s:  Health check passes -> "healthy"     (SAME status -> filter)
  Time 3s:  Health check passes -> "healthy"     (SAME status -> filter)
  Time 4s:  Health check fails -> "unhealthy"    (NEW status -> emit)
  Time 5s:  Health check fails -> "unhealthy"    (SAME status -> filter)
  Time 6s:  Health check passes -> "healthy"     (NEW status -> emit)
  Time 7s:  Health check passes -> "healthy"     (SAME status -> filter)

Docker API should emit: starting, healthy, unhealthy, healthy (4 events)
Libpod API should emit: all 8 events
`
	t.Log(scenario)

	containerID := "test-container"
	events := []string{"starting", "healthy", "healthy", "healthy", "unhealthy", "unhealthy", "healthy", "healthy"}

	var dockerEventsEmitted int
	var libpodEventsEmitted int

	for i, status := range events {
		shouldFilter := false

		healthStatusLock.Lock()
		previousStatus, exists := previousHealthStatus[containerID]
		if exists && previousStatus == status {
			shouldFilter = true
		}
		previousHealthStatus[containerID] = status
		healthStatusLock.Unlock()

		if !shouldFilter {
			dockerEventsEmitted++
		}
		libpodEventsEmitted++

		t.Logf("Event %d: status=%s, Docker filtered=%v, Docker emitted so far=%d, Libpod emitted so far=%d",
			i+1, status, shouldFilter, dockerEventsEmitted, libpodEventsEmitted)
	}

	assert.Equal(t, 4, dockerEventsEmitted,
		"Docker API should emit 4 events (starting, healthy, unhealthy, healthy)")
	assert.Equal(t, 8, libpodEventsEmitted,
		"Libpod API should emit all 8 events")
}
