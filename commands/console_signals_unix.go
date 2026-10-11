//go:build !windows

package commands

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/terminal"
)

// consoleStopSignals are the signals that end an interactive console session.
// SIGHUP covers a terminal that goes away; Windows defines it but never
// delivers it, so it is listed only here.
func consoleStopSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGHUP}
}

// watchResize forwards window-size changes to resizes until the returned stop
// function is called. A resized terminal sends SIGWINCH.
func watchResize(fd int, resizes chan<- terminal.Size) func() {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-winch:
				if w, h, err := term.GetSize(fd); err == nil {
					select {
					case resizes <- terminal.Size{Cols: w, Rows: h}:
					default:
					}
				}
			}
		}
	}()
	return func() {
		signal.Stop(winch)
		close(done)
	}
}
