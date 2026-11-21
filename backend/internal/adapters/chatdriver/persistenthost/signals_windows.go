//go:build windows

package persistenthost

import "os"

// hostShutdownSignals end a chat host through its orderly teardown.
var hostShutdownSignals = []os.Signal{os.Interrupt}
