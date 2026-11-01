package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

// `miosa engine maintain` drives the control plane's maintenance proxy
// (POST /databases/:id/maintenance). The route allowlists three Engine tasks
// and calls the Engine with its admin key server-side, so the key and the
// Engine URL never reach this command. embed_backfill and corpus_organization
// answer with their result; reindex is queued on the Engine and answers with
// its status and job id. The platform exposes no job status, so there is no
// --wait to offer.

var engineMaintenanceTasks = []string{"embed_backfill", "corpus_organization", "reindex"}

type engineMaintainOptions struct {
	engine           string
	task             string
	limit            int
	workspace        string
	apply            bool
	queueRemaining   bool
	deduplicateExact bool
}

func newEngineMaintainCmd() *cobra.Command {
	var opts engineMaintainOptions

	cmd := &cobra.Command{
		Use:   "maintain <engine> <task>",
		Short: "Run an Engine maintenance task: embed_backfill, corpus_organization or reindex",
		Long: `Runs one of the Engine's own maintenance tasks through the control plane, which
attaches the Engine's admin key server-side. The Engine must be running.
embed_backfill and corpus_organization answer with their result; reindex is
queued on the Engine and prints its status and job id.

Tasks:
  embed_backfill       Embed stored text that has no vector yet.
                       --limit caps how many (the Engine clamps 1..1000, default 100).
  corpus_organization  Organize the corpus. Exactly one mode:
                       --apply, --queue-remaining or --deduplicate-exact.
  reindex              Rebuild the search index for a workspace.

--workspace is the ENGINE workspace id (default:<slug>), not the MIOSA
workspace id. 'miosa engine get <engine> --json' shows the attached workspaces.

  miosa engine maintain qa-brain embed_backfill --limit 200
  miosa engine maintain qa-brain corpus_organization --deduplicate-exact
  miosa engine maintain qa-brain reindex --workspace default:team-a

The key needs the engine:write scope.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.engine = args[0]
			opts.task = args[1]
			return runEngineMaintain(cmd, opts)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.limit, "limit", 0, "How many items to embed (embed_backfill, 1..1000)")
	flags.StringVar(&opts.workspace, "workspace", "", "Engine workspace id (default:<slug>)")
	flags.BoolVar(&opts.apply, "apply", false, "Apply the corpus organization plan")
	flags.BoolVar(&opts.queueRemaining, "queue-remaining", false, "Queue the remaining corpus work")
	flags.BoolVar(&opts.deduplicateExact, "deduplicate-exact", false, "Remove exact duplicates from the corpus")
	return cmd
}

// maintainBody validates the flags against the chosen task and builds the
// request body. Every task takes a different set, so a flag the task does not
// use is a usage error rather than something silently dropped.
func maintainBody(opts engineMaintainOptions) (map[string]any, error) {
	body := map[string]any{"task": opts.task}

	switch opts.task {
	case "embed_backfill":
		if opts.workspace != "" {
			body["workspace"] = opts.workspace
		}
		if opts.limit != 0 {
			if opts.limit < 1 || opts.limit > 1000 {
				return nil, usagef("--limit must be between 1 and 1000 (got %d)", opts.limit)
			}
			body["limit"] = opts.limit
		}
		if err := rejectCorpusFlags(opts); err != nil {
			return nil, err
		}

	case "corpus_organization":
		modes := 0
		if opts.apply {
			modes++
		}
		if opts.queueRemaining {
			modes++
		}
		if opts.deduplicateExact {
			modes++
		}
		if modes != 1 {
			return nil, usagef("corpus_organization takes exactly one of --apply, --queue-remaining or --deduplicate-exact")
		}
		if opts.limit != 0 || opts.workspace != "" {
			return nil, usagef("corpus_organization takes no --limit or --workspace")
		}
		switch {
		case opts.apply:
			body["apply"] = true
		case opts.queueRemaining:
			body["queue_remaining"] = true
		default:
			body["deduplicate_exact"] = true
		}

	case "reindex":
		if opts.workspace != "" {
			body["workspace"] = opts.workspace
		}
		if opts.limit != 0 {
			return nil, usagef("reindex takes no --limit")
		}
		if err := rejectCorpusFlags(opts); err != nil {
			return nil, err
		}

	default:
		return nil, usagef("task must be one of: %s (got %q)", strings.Join(engineMaintenanceTasks, ", "), opts.task)
	}

	return body, nil
}

func rejectCorpusFlags(opts engineMaintainOptions) error {
	if opts.apply || opts.queueRemaining || opts.deduplicateExact {
		return usagef("%s takes no --apply, --queue-remaining or --deduplicate-exact", opts.task)
	}
	return nil
}

func runEngineMaintain(cmd *cobra.Command, opts engineMaintainOptions) error {
	body, err := maintainBody(opts)
	if err != nil {
		return err
	}

	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	engine, err := resolveManagedEngine(ctx, c.API.Get, opts.engine)
	if err != nil {
		return die(err)
	}

	var result any
	if err := c.API.Post(ctx, "/databases/"+engine.ID+"/maintenance", body, &result); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(result)
	}
	p.Line("%s: %s accepted", engine.Name, opts.task)
	return renderReadable(p, result)
}
