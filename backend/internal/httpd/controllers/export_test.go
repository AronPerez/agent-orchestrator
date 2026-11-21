package controllers

import (
	"testing"
	"time"
)

// SetProjectOverlapStreamPollIntervalForTest shortens the overlap stream's
// rescan interval for one test.
func SetProjectOverlapStreamPollIntervalForTest(t *testing.T, interval time.Duration) {
	t.Helper()
	previous := projectOverlapStreamPollInterval
	projectOverlapStreamPollInterval = interval
	t.Cleanup(func() { projectOverlapStreamPollInterval = previous })
}
