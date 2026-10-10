package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// sseTicket obtains a one-hour ticket for streaming endpoints, which cannot
// take an Authorization header from a browser and so use ?ticket=.
func sseTicket(ctx context.Context, c *api.Client) (string, error) {
	var out struct {
		Ticket string `json:"ticket"`
	}
	if err := c.Post(ctx, "/sse/ticket", map[string]any{}, &out); err != nil {
		return "", err
	}
	if out.Ticket == "" {
		return "", errors.New("the server returned no stream ticket")
	}
	return out.Ticket, nil
}

// followStream reads a server-sent-event endpoint until the user interrupts
// it or the server ends the stream. A dropped connection is retried a few
// times with a fresh ticket.
func followStream(cmd *cobra.Command, c *api.Client, path string, q url.Values, fn func(api.Event) error) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		ticket, err := sseTicket(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return die(err)
		}
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("ticket", ticket)
		err = c.Events(ctx, path, qq, fn)
		switch {
		case err == nil:
			return nil
		case ctx.Err() != nil:
			return nil // interrupted
		}
		var te *api.TransportError
		if errors.As(err, &te) || errors.Is(err, io.ErrUnexpectedEOF) {
			lastErr = err
			continue
		}
		return die(err)
	}
	return die(lastErr)
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// rawOrString returns data as parsed JSON when it is JSON, else as a string.
func rawOrString(data string) any {
	var v any
	if json.Unmarshal([]byte(data), &v) == nil {
		return v
	}
	return data
}

func sortRows(rows [][]string) {
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
}
