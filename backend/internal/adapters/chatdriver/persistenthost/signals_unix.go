//go:build !windows

package persistenthost

import (
	"os"
	"syscall"
)

// hostShutdownSignals end a chat host through its orderly teardown, which
// stops the provider tree and clears the launch records.
var hostShutdownSignals = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP}
