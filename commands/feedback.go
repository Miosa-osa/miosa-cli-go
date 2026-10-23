package commands

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newFeedbackCmd())
}

// feedbackTypes are the kinds the feedback API accepts.
var feedbackTypes = map[string]bool{"bug": true, "feature": true, "improvement": true, "general": true}

// maxFeedbackLength matches the API's limit on a message.
const maxFeedbackLength = 5000

func newFeedbackCmd() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "feedback <message>",
		Short: "Send feedback to the MIOSA team",
		Long: `Send a message straight to the MIOSA team: a bug, a missing feature, or anything
that got in your way. Every message is emailed to the team and read by a person,
and replies come to your account's email address.

The CLI version and your operating system are attached so the team can
reproduce what you saw. Pass - to read the message from standard input.

  miosa feedback "miosa deploy hangs after the build finishes"
  miosa feedback --type feature "let me pick the region on create"
  miosa logs my-box | miosa feedback --type bug -`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeedback(cmd, args, kind)
		},
	}
	cmd.Flags().StringVarP(&kind, "type", "t", "general", "Kind of feedback: bug, feature, improvement or general")
	return cmd
}

func runFeedback(cmd *cobra.Command, args []string, kind string) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !feedbackTypes[kind] {
		return usagef("unknown --type %q (use bug, feature, improvement or general)", kind)
	}

	message := strings.Join(args, " ")
	if len(args) == 1 && args[0] == "-" {
		b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxFeedbackLength*4+1))
		if err != nil {
			return die(fmt.Errorf("reading standard input: %w", err))
		}
		message = string(b)
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return usagef("the message is empty")
	}
	if n := len([]rune(message)); n > maxFeedbackLength {
		return usagef("the message is %d characters; the limit is %d", n, maxFeedbackLength)
	}

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	body := map[string]any{
		"type":        kind,
		"message":     message,
		"source":      "cli",
		"app_version": "cli-" + cliVersion,
		"user_agent":  "miosa-cli/" + cliVersion + " (" + platform + ")",
		"metadata": map[string]any{
			"source":      "cli",
			"cli_version": cliVersion,
			"os":          runtime.GOOS,
			"arch":        runtime.GOARCH,
		},
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	// No retries: a 429 here is the per-user feedback limit (minutes to a day),
	// not a blip worth waiting 90 seconds on.
	once := *c.API
	once.MaxRetries = 0
	if err := once.Post(cmd.Context(), "/feedback", body, &out); err != nil {
		if ae, ok := asAPIError(err); ok && ae.Status == 429 {
			return die(fmt.Errorf("you sent a lot of feedback in a short time; try again later, or email support@miosa.ai"))
		}
		return die(err)
	}

	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(out)
	}
	p.Success("Feedback sent (%s). The team reads every message and replies by email.", out.ID)
	return nil
}
