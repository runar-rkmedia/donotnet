//go:build windows

package runner

import (
	"os"
)

var shutdownSignals = []os.Signal{os.Interrupt}
