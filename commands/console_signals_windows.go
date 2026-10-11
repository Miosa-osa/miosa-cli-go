//go:build windows

package commands

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/terminal"
)

// consoleStopSignals are the signals that end an interactive console session.
// Windows has no SIGHUP.
func consoleStopSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM}
}

// watchResize forwards window-size changes to resizes until the returned stop
// function is called. Windows has no SIGWINCH, so the size is polled instead;
// the check only runs while a console session is open.
func watchResize(fd int, resizes chan<- terminal.Size) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		var last terminal.Size
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				w, h, err := term.GetSize(fd)
				if err != nil {
					continue
				}
				size := terminal.Size{Cols: w, Rows: h}
				if size == last {
					continue
				}
				last = size
				select {
				case resizes <- size:
				default:
				}
			}
		}
	}()
	return func() { close(done) }
}
