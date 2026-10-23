package commands

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// A --wait that prints nothing for minutes reads as a hang. waitProgress shows
// what the wait is on, on standard error so standard output stays the result:
// on a terminal one line redraws (a spinner, the state, the time so far) and is
// cleared when the wait ends; off a terminal a line is printed each time the
// state changes; with --json or --quiet nothing is printed.

// stderrIsTerminal is outputIsTerminal for the progress line; tests replace it.
var stderrIsTerminal = func(w io.Writer) bool { return outputIsTerminal(w) }

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type waitProgress struct {
	w       io.Writer
	what    string
	tty     bool
	started time.Time

	mu      sync.Mutex
	state   string
	frame   int
	stop    chan struct{}
	done    sync.WaitGroup
	stopped sync.Once
}

// startWaitProgress begins showing progress for what ("my-box to be ready").
// Call Set with each polled state and Stop when the wait ends.
func startWaitProgress(cmd *cobra.Command, what string) *waitProgress {
	wp := &waitProgress{w: cmd.ErrOrStderr(), what: what, started: time.Now()}
	if isJSON() || globalFlags.Quiet {
		wp.w = nil
		return wp
	}
	wp.tty = stderrIsTerminal(wp.w)
	if !wp.tty {
		fmt.Fprintf(wp.w, "Waiting for %s\n", what)
		return wp
	}
	wp.stop = make(chan struct{})
	wp.done.Add(1)
	go func() {
		defer wp.done.Done()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-wp.stop:
				return
			case <-tick.C:
				wp.mu.Lock()
				wp.draw()
				wp.mu.Unlock()
			}
		}
	}()
	return wp
}

// Set records the latest state; off a terminal a change prints one line.
func (wp *waitProgress) Set(state string) {
	if wp == nil || wp.w == nil {
		return
	}
	wp.mu.Lock()
	defer wp.mu.Unlock()
	if state == wp.state {
		return
	}
	wp.state = state
	if !wp.tty {
		fmt.Fprintf(wp.w, "  %s (%s)\n", orDash(state), wp.elapsed())
	}
}

// Stop ends the progress and clears the terminal line. It may be called more
// than once, and on a nil progress (no --wait).
func (wp *waitProgress) Stop() {
	if wp == nil || wp.w == nil || !wp.tty {
		return
	}
	wp.stopped.Do(func() {
		close(wp.stop)
		wp.done.Wait()
		fmt.Fprint(wp.w, "\r\033[K")
	})
}

func (wp *waitProgress) draw() {
	wp.frame = (wp.frame + 1) % len(spinnerFrames)
	line := fmt.Sprintf("%s Waiting for %s", spinnerFrames[wp.frame], wp.what)
	if wp.state != "" {
		line += ": " + wp.state
	}
	fmt.Fprintf(wp.w, "\r\033[K%s (%s)", line, wp.elapsed())
}

func (wp *waitProgress) elapsed() string {
	return time.Since(wp.started).Round(time.Second).String()
}
