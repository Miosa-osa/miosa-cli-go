package commands

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func progressCmd(buf *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{}
	c.SetErr(buf)
	return c
}

func withStderrTerminal(t *testing.T, tty bool) {
	t.Helper()
	prev := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return tty }
	t.Cleanup(func() { stderrIsTerminal = prev })
}

func TestWaitProgressOffATerminalPrintsStateChanges(t *testing.T) {
	ResetForTest()
	withStderrTerminal(t, false)
	var buf bytes.Buffer
	wp := startWaitProgress(progressCmd(&buf), "box to be ready")
	wp.Set("creating")
	wp.Set("creating")
	wp.Set("booting")
	wp.Stop()
	got := buf.String()
	if !strings.HasPrefix(got, "Waiting for box to be ready\n") {
		t.Fatalf("out = %q", got)
	}
	if strings.Count(got, "creating") != 1 || !strings.Contains(got, "  booting (") {
		t.Fatalf("out = %q", got)
	}
}

func TestWaitProgressOnATerminalRedrawsAndClears(t *testing.T) {
	ResetForTest()
	withStderrTerminal(t, true)
	var buf syncBuffer
	c := &cobra.Command{}
	c.SetErr(&buf)
	wp := startWaitProgress(c, "box to be ready")
	wp.Set("booting")
	time.Sleep(250 * time.Millisecond)
	wp.Stop()
	got := buf.String()
	if !strings.Contains(got, "Waiting for box to be ready: booting") || !strings.HasSuffix(got, "\r\033[K") {
		t.Fatalf("out = %q", got)
	}
}

func TestWaitProgressSilentWithJSON(t *testing.T) {
	ResetForTest()
	withStderrTerminal(t, false)
	globalFlags.Output = "json"
	defer ResetForTest()
	var buf bytes.Buffer
	wp := startWaitProgress(progressCmd(&buf), "box to be ready")
	wp.Set("booting")
	wp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("out = %q", buf.String())
	}
}

// syncBuffer is a bytes.Buffer safe for the spinner goroutine and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestWaitProgressStopTwiceAndNil(t *testing.T) {
	ResetForTest()
	withStderrTerminal(t, true)
	var buf syncBuffer
	c := &cobra.Command{}
	c.SetErr(&buf)
	wp := startWaitProgress(c, "box")
	wp.Stop()
	wp.Stop()
	var none *waitProgress
	none.Set("x")
	none.Stop()
	if strings.Count(buf.String(), "\r\033[K") < 1 {
		t.Fatalf("out = %q", buf.String())
	}
}
