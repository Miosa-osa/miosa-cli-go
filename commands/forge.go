package commands

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	miosa "github.com/Miosa-osa/miosa-go"
)

type forgeOutputOptions struct{ JSON bool }

var forgeInputIsTerminal = func(input io.Reader) bool {
	file, ok := input.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func newForgeCmd() *cobra.Command {
	outputOptions := &forgeOutputOptions{}
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Work with MIOSA Forge repositories (beta)",
		Long:  "Work with repositories owned by the organization associated with the current MIOSA credential.",
	}
	cmd.PersistentFlags().BoolVar(&outputOptions.JSON, "json", false, "Output a stable JSON envelope")
	repo := &cobra.Command{Use: "repo", Short: "Manage Forge repositories"}
	repo.AddCommand(
		newForgeRepoListCmd(outputOptions),
		newForgeRepoCreateCmd(outputOptions),
		newForgeRepoShowCmd(outputOptions),
		newForgeRepoUpdateCmd(outputOptions),
		newForgeRepoDeleteCmd(outputOptions),
	)
	cmd.AddCommand(repo)
	cmd.AddCommand(forgeCollabCmds()...)
	return cmd
}

func forgeJSON(options *forgeOutputOptions) bool { return options.JSON || isJSON() }

func newForgeRepoListCmd(outputOptions *forgeOutputOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List repositories in the current organization",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := buildClient()
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			repositories, err := client.SDK.Forge.List(cmd.Context())
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			p := printerFor(cmd)
			if forgeJSON(outputOptions) {
				return p.JSON(map[string]interface{}{"data": repositories})
			}
			rows := make([][]string, 0, len(repositories))
			for _, repository := range repositories {
				rows = append(rows, []string{repository.Name, repository.Slug, repository.Visibility, repository.State, repository.ID})
			}
			p.Table([]string{"NAME", "SLUG", "VISIBILITY", "STATE", "ID"}, rows)
			return nil
		},
	}
}

func newForgeRepoCreateCmd(outputOptions *forgeOutputOptions) *cobra.Command {
	var slug, branch, visibility, idempotencyKey string
	var projectIDs []string
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a repository in the current organization",
		Args:    cobra.ExactArgs(1),
		Example: "  miosa forge repo create platform --visibility private\n  miosa forge repo create platform --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if visibility != "public" && visibility != "private" && visibility != "internal" {
				return forgeFail(cmd, outputOptions, fmt.Errorf("visibility must be public, private, or internal"))
			}
			if idempotencyKey == "" {
				generatedKey, keyErr := newForgeIdempotencyKey()
				if keyErr != nil {
					return forgeFail(cmd, outputOptions, keyErr)
				}
				idempotencyKey = generatedKey
			}
			client, _, err := buildClient()
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			repository, err := client.SDK.Forge.Create(cmd.Context(), miosa.CreateForgeRepositoryInput{
				Name: args[0], Slug: slug, DefaultBranch: branch, Visibility: visibility,
				ProjectIDs: projectIDs, IdempotencyKey: idempotencyKey,
			})
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			p := printerFor(cmd)
			if forgeJSON(outputOptions) {
				return p.JSON(map[string]interface{}{"data": repository})
			}
			p.Success("Created repository %q in the current organization", repository.Slug)
			p.Line("  ID:         %s", repository.ID)
			p.Line("  Visibility: %s", repository.Visibility)
			p.Line("  Clone URL:  %s", forgeCloneURL(repository.CloneURL))
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "Repository URL slug")
	cmd.Flags().StringVar(&branch, "default-branch", "main", "Default branch name")
	cmd.Flags().StringVar(&visibility, "visibility", "private", "Repository visibility: public, private, or internal")
	cmd.Flags().StringSliceVar(&projectIDs, "project-id", nil, "Attach a project ID (repeatable)")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Stable key for safely retrying this mutation")
	return cmd
}

func newForgeRepoShowCmd(outputOptions *forgeOutputOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "show <repository-id>",
		Short: "Show a repository in the current organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := buildClient()
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			repository, err := client.SDK.Forge.Get(cmd.Context(), args[0])
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			p := printerFor(cmd)
			if forgeJSON(outputOptions) {
				return p.JSON(map[string]interface{}{"data": repository})
			}
			p.Table([]string{"FIELD", "VALUE"}, [][]string{
				{"Name", repository.Name}, {"Slug", repository.Slug}, {"ID", repository.ID},
				{"Default branch", repository.DefaultBranch}, {"Visibility", repository.Visibility},
				{"State", repository.State}, {"Clone URL", forgeCloneURL(repository.CloneURL)},
			})
			return nil
		},
	}
}

func newForgeRepoUpdateCmd(outputOptions *forgeOutputOptions) *cobra.Command {
	var name, slug, visibility string
	var projectIDs []string
	cmd := &cobra.Command{Use: "update <repository-id>", Short: "Update a repository", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if visibility != "" && visibility != "public" && visibility != "private" && visibility != "internal" {
			return forgeFail(cmd, outputOptions, fmt.Errorf("visibility must be public, private, or internal"))
		}
		client, _, err := buildClient()
		if err != nil {
			return forgeFail(cmd, outputOptions, err)
		}
		repository, err := client.SDK.Forge.Update(cmd.Context(), args[0], miosa.UpdateForgeRepositoryInput{Name: name, Slug: slug, Visibility: visibility, ProjectIDs: projectIDs})
		if err != nil {
			return forgeFail(cmd, outputOptions, err)
		}
		p := printerFor(cmd)
		if forgeJSON(outputOptions) {
			return p.JSON(map[string]interface{}{"data": repository})
		}
		p.Success("Updated repository %q", repository.ID)
		return nil
	}}
	cmd.Flags().StringVar(&name, "name", "", "Repository display name")
	cmd.Flags().StringVar(&slug, "slug", "", "Repository URL slug")
	cmd.Flags().StringVar(&visibility, "visibility", "", "Repository visibility")
	cmd.Flags().StringSliceVar(&projectIDs, "project-id", nil, "Replace attached project IDs")
	return cmd
}

func forgeCloneURL(value *string) string {
	if value == nil {
		return "Not ready"
	}
	return *value
}

func newForgeRepoDeleteCmd(outputOptions *forgeOutputOptions) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <repository-id>",
		Short: "Permanently delete a repository",
		Long:  "Permanently delete a Forge repository. Non-interactive use requires --yes.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				if !forgeInputIsTerminal(cmd.InOrStdin()) {
					return forgeFail(cmd, outputOptions, fmt.Errorf("confirmation required; re-run with --yes for non-interactive use"))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Type %s to permanently delete this repository: ", args[0])
				answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && strings.TrimSpace(answer) == "" {
					return forgeFail(cmd, outputOptions, fmt.Errorf("confirmation required; re-run with --yes for non-interactive use"))
				}
				if strings.TrimSpace(answer) != args[0] {
					return forgeFail(cmd, outputOptions, fmt.Errorf("repository deletion cancelled"))
				}
			}
			client, _, err := buildClient()
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			receipt, err := client.SDK.Forge.Delete(cmd.Context(), args[0])
			if err != nil {
				return forgeFail(cmd, outputOptions, err)
			}
			p := printerFor(cmd)
			result := map[string]interface{}{"id": args[0], "status": "deleted", "operation_id": receipt.OperationID, "replayed": receipt.Replayed}
			if forgeJSON(outputOptions) {
				return p.JSON(map[string]interface{}{"data": result})
			}
			p.Success("Deleted repository %q", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm permanent deletion without prompting")
	return cmd
}

func newForgeIdempotencyKey() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	return "forge-cli-" + hex.EncodeToString(value), nil
}

func forgeFail(cmd *cobra.Command, options *forgeOutputOptions, err error) error {
	if !forgeJSON(options) {
		return die(err)
	}
	code, retryable := "FORGE_ERROR", false
	var base *miosa.MiosaError
	switch value := err.(type) {
	case *miosa.ForgeUnavailableError:
		base = &value.MiosaError
	case *miosa.ForgeStorageError:
		base, retryable = &value.MiosaError, true
	case *miosa.ForgePolicyViolationError:
		base = &value.MiosaError
	}
	if base != nil && base.Code != "" {
		code = base.Code
	}
	_ = printerFor(cmd).JSON(map[string]interface{}{
		"ok":    false,
		"error": map[string]interface{}{"code": code, "message": err.Error(), "retryable": retryable},
	})
	return fmt.Errorf("command failed")
}
