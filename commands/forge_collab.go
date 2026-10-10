package commands

import (
	"github.com/spf13/cobra"
)

// forgeCollabCmds are the collaboration commands of Forge: pull requests,
// reviews, checks, releases and branch policy. <repo> is a repository id,
// slug or name. Every change is replay-safe: --idempotency-key repeats it
// without doing it twice, and one is generated when you give none.
func forgeCollabCmds() []*cobra.Command {
	repo := Arg{Name: "repo"}
	number := Arg{Name: "number"}

	prCols := []Col{
		{Head: "#", Path: "number"}, {Head: "STATE", Path: "state"}, {Head: "TITLE", Path: "title"},
		{Head: "FROM", Path: "head_branch"}, {Head: "INTO", Path: "base_branch"}, {Head: "AUTHOR", Path: "author.name"},
	}
	prDetail := []Col{
		{Head: "Number", Path: "number"}, {Head: "Title", Path: "title"}, {Head: "State", Path: "state"},
		{Head: "From", Path: "head_branch"}, {Head: "Into", Path: "base_branch"}, {Head: "Head", Path: "head_sha"},
		{Head: "Author", Path: "author.name"}, {Head: "Id", Path: "id"},
	}
	checkFlags := []Flag{
		{Name: "name", Usage: "Check name, for example unit-tests"},
		{Name: "status", Usage: "Queued, in_progress or completed (default completed)"},
		{Name: "conclusion", Usage: "Passed or failed, for a completed check"},
		{Name: "summary", Usage: "One line on what the check found"},
		{Name: "details-url", Usage: "Link to the full report"},
	}

	pr := group("pr", "Pull requests, reviews, checks and merging",
		`<repo> is a repository id, slug or name; <number> is the pull request number.
Reviewing and merging need the write role on the repository, and the branch
policy decides what a merge needs: approvals, passing checks, and a reviewer other
than the author.

  miosa forge pr new platform --head fix-redirect --title "Fix redirect loop"
  miosa forge pr list platform
  miosa forge pr show platform 12
  miosa forge pr review platform 12 --approve
  miosa forge pr merge platform 12`, []string{"pulls", "pull-request", "pull-requests"},
		Op{Use: "list <repo>", Aliases: []string{"ls"}, Short: "List pull requests", Method: "GET", Path: "/forge/repositories/{0}/pull-requests", Args: []Arg{repo}, Cols: prCols},
		Op{
			Use: "new <repo>", Aliases: []string{"create", "open"}, Short: "Open a pull request", Method: "POST", Path: "/forge/repositories/{0}/pull-requests",
			Args: []Arg{repo}, Idempotent: true, Detail: prDetail,
			Flags: []Flag{
				{Name: "head", Usage: "Branch with the changes", Field: "head_branch", Required: true},
				{Name: "base", Usage: "Branch to merge into (default: the repository's default branch)", Field: "base_branch"},
				{Name: "title", Usage: "Title", Required: true},
				{Name: "body", Usage: "Description"},
				{Name: "draft", Usage: "Open it as a draft", Type: "bool"},
				{Name: "head-repo", Usage: "Repository id the head branch lives in, for a pull request from a fork", Field: "head_repository_id"},
			},
		},
		Op{
			Use: "show <repo> <number>", Aliases: []string{"get", "status"}, Short: "Show a pull request with its reviews, checks and merge readiness",
			Method: "GET", Path: "/forge/repositories/{0}/pull-requests/{1}/collaboration", Args: []Arg{repo, number},
		},
		Op{
			Use: "review <repo> <number>", Short: "Approve, request changes on, or comment on a pull request",
			Long:   "Records a review of the pull request at its current head. You cannot approve your own pull request.",
			Method: "POST", Path: "/forge/repositories/{0}/pull-requests/{1}/reviews", Args: []Arg{repo, number}, Idempotent: true,
			Flags: []Flag{
				{Name: "decision", Usage: "What the review says", Required: true, Enum: []string{"approved", "changes_requested", "commented"}},
				{Name: "body", Usage: "Review comment"},
			},
		},
		Op{
			Use: "check <repo> <number>", Short: "Report a check result on a pull request's head commit",
			Long: `Reports a check against the pull request's current head commit. The check needs
evidence: put {"evidence": {...}} in --file. The reporter must not be the author,
and --head-sha must equal the pull request's head, so a stale result is refused.`,
			Method: "POST", Path: "/forge/repositories/{0}/pull-requests/{1}/check-runs", Args: []Arg{repo, number}, Idempotent: true, FileBody: true,
			Flags: append([]Flag{{Name: "head-sha", Usage: "The pull request's current head commit", Field: "head_sha"}}, checkFlags[:3]...),
		},
		Op{
			Use: "merge <repo> <number>", Short: "Merge a pull request",
			Long:   "Merges when the branch policy is met: enough approvals, required checks passing, and the base branch not moved. A merge into a branch that deploys on merge starts that deployment.",
			Method: "POST", Path: "/forge/repositories/{0}/pull-requests/{1}/merge", Args: []Arg{repo, number}, Idempotent: true,
		},
		Op{Use: "close <repo> <number>", Short: "Close a pull request without merging", Method: "POST", Path: "/forge/repositories/{0}/pull-requests/{1}/close", Args: []Arg{repo, number}, Detail: prDetail},
		Op{Use: "reopen <repo> <number>", Short: "Reopen a closed pull request", Method: "POST", Path: "/forge/repositories/{0}/pull-requests/{1}/reopen", Args: []Arg{repo, number}, Detail: prDetail},
		Op{
			Use: "comments <repo> <number>", Short: "List the comments on a pull request", Method: "GET", Path: "/forge/repositories/{0}/pull-requests/{1}/comments", Args: []Arg{repo, number},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "FROM", Path: "author.name"}, {Head: "COMMENT", Path: "body"}, {Head: "AGE", Path: "inserted_at", Fmt: "age"}},
		},
		Op{
			Use: "comment <repo> <number>", Short: "Add a comment to a pull request", Method: "POST", Path: "/forge/repositories/{0}/pull-requests/{1}/comments",
			Args: []Arg{repo, number}, Idempotent: true, Flags: []Flag{{Name: "body", Usage: "Comment text", Required: true}},
		},
		Op{
			Use: "previews <repo> <number>", Short: "List the preview deployments of a pull request", Method: "GET", Path: "/forge/repositories/{0}/pull-requests/{1}/previews", Args: []Arg{repo, number},
		},
	)

	check := group("check", "Commit checks: results reported against a commit",
		`A check is a named result (unit-tests passed, lint failed) recorded against a
commit, with the evidence behind it. Branch policies can require named checks
before a pull request merges. 'miosa forge pr check' reports one against a pull
request's head; these commands work on any commit.

  miosa forge check list platform
  miosa forge check suite platform <commit>
  miosa forge check report platform --commit <sha> --name unit-tests --conclusion passed --file evidence.json`, []string{"checks"},
		Op{Use: "list <repo>", Aliases: []string{"ls"}, Short: "List recent check suites, newest first", Method: "GET", Path: "/forge/repositories/{0}/check-suites", Args: []Arg{repo},
			Flags: []Flag{{Name: "limit", Type: "int", Usage: "How many suites"}, {Name: "commit", Usage: "Only this commit", Field: "commit_sha"}}},
		Op{Use: "suite <repo> <commit>", Short: "Show every check on one commit", Method: "GET", Path: "/forge/repositories/{0}/check-suites/{1}", Args: []Arg{repo, {Name: "commit"}}},
		Op{Use: "show <repo> <check>", Aliases: []string{"get"}, Short: "Show one check", Method: "GET", Path: "/forge/repositories/{0}/check-runs/{1}", Args: []Arg{repo, {Name: "check"}}},
		Op{Use: "evidence <repo> <check>", Short: "Show the evidence behind a check", Method: "GET", Path: "/forge/repositories/{0}/check-runs/{1}/evidence", Args: []Arg{repo, {Name: "check"}}},
		Op{
			Use: "report <repo>", Short: "Report a check result against a commit",
			Long:   "A completed check needs evidence: put {\"evidence\": {...}} in --file (up to 64 KB). Repeating an --idempotency-key returns the first result.",
			Method: "POST", Path: "/forge/repositories/{0}/check-runs", Args: []Arg{repo}, Idempotent: true, FileBody: true,
			Flags:  append([]Flag{{Name: "commit", Usage: "Full commit SHA the result is for", Field: "commit_sha", Required: true}}, checkFlags...),
			Detail: []Col{{Head: "Id", Path: "id"}, {Head: "Name", Path: "name"}, {Head: "Status", Path: "status"}, {Head: "Result", Path: "conclusion"}, {Head: "Commit", Path: "commit_sha"}},
		},
	)

	release := group("release", "Releases: a tag with notes and downloadable assets",
		`<tag> is the release's tag name.

  miosa forge release list platform
  miosa forge release new platform --tag v1.2.0 --name "1.2.0" --body "Fixes the redirect"`, []string{"releases"},
		Op{
			Use: "list <repo>", Aliases: []string{"ls"}, Short: "List releases", Method: "GET", Path: "/forge/repositories/{0}/releases", Args: []Arg{repo},
			Cols: []Col{{Head: "TAG", Path: "tag_name"}, {Head: "NAME", Path: "name"}, {Head: "PRERELEASE", Path: "prerelease"}, {Head: "AGE", Path: "inserted_at", Fmt: "age"}},
		},
		Op{Use: "show <repo> <tag>", Aliases: []string{"get"}, Short: "Show one release and its assets", Method: "GET", Path: "/forge/repositories/{0}/releases/{1}", Args: []Arg{repo, {Name: "tag"}}},
		Op{
			Use: "new <repo>", Aliases: []string{"create"}, Short: "Create a release", Method: "POST", Path: "/forge/repositories/{0}/releases", Args: []Arg{repo}, Idempotent: true,
			Flags: []Flag{
				{Name: "tag", Usage: "Tag name, created if it does not exist", Required: true},
				{Name: "commit", Usage: "Commit or branch to tag (default: the default branch)", Field: "commit_sha"},
				{Name: "name", Usage: "Release title"},
				{Name: "body", Usage: "Release notes"},
				{Name: "prerelease", Usage: "Mark it as a prerelease", Type: "bool"},
			},
		},
		Op{
			Use: "tag <repo>", Short: "Create a tag", Method: "POST", Path: "/forge/repositories/{0}/tags", Args: []Arg{repo},
			Flags: []Flag{
				{Name: "name", Usage: "Tag name", Required: true},
				{Name: "target", Usage: "Commit or branch to tag (default: the default branch)"},
				{Name: "message", Usage: "Annotation for the tag"},
			},
		},
	)

	policy := group("policy", "Branch policy and merge policy of a repository",
		`A branch policy says what a merge into a branch needs. Publishing one creates a
new version; the newest applies.

  miosa forge policy set platform --branch main --approvals 2 --check unit-tests
  miosa forge policy show platform
  miosa forge policy owners platform`, nil,
		Op{Use: "show <repo>", Short: "Show the merge policy", Method: "GET", Path: "/forge/repositories/{0}/merge-policy", Args: []Arg{repo}},
		Op{Use: "owners <repo>", Short: "Show the code owners", Method: "GET", Path: "/forge/repositories/{0}/code-owners", Args: []Arg{repo}},
		Op{
			Use: "set <repo>", Short: "Publish a branch policy", Method: "POST", Path: "/forge/repositories/{0}/branch-policies", Args: []Arg{repo}, Idempotent: true,
			Long: "Needs the maintain role. Without --approvals one approval is required; the author cannot approve their own pull request unless --independent=false.",
			Flags: []Flag{
				{Name: "branch", Usage: "Branch the policy covers (default: the default branch)"},
				{Name: "approvals", Usage: "Approvals required", Type: "int", Field: "required_approvals"},
				{Name: "check", Usage: "A check that must pass (repeatable)", Type: "strings", Field: "required_checks"},
				{Name: "independent", Usage: "Require an approver other than the author", Type: "bool", Field: "require_independent_approval", Default: "true"},
				{Name: "dismiss-stale", Usage: "New commits dismiss earlier approvals", Type: "bool", Field: "dismiss_stale_reviews", Default: "true"},
				{Name: "code-owners", Usage: "Require a code owner's approval", Type: "bool", Field: "require_code_owner_approval"},
			},
		},
	)

	access := group("grant", "Repository roles for people", "Roles are read, write, maintain and admin.", nil,
		Op{
			Use: "set <repo> <user-id>", Short: "Give a person a role on a repository", Method: "PUT", Path: "/forge/repositories/{0}/grants/{1}", Args: []Arg{repo, {Name: "user-id"}}, Idempotent: true,
			Flags: []Flag{{Name: "role", Usage: "Read, write, maintain or admin", Required: true}},
		},
	)
	fork := Op{
		Use: "fork <repo>", Short: "Fork a repository into your organization", Method: "POST", Path: "/forge/repositories/{0}/forks", Args: []Arg{repo}, Idempotent: true,
		Flags: []Flag{{Name: "name", Usage: "Name for the fork", Required: true}, {Name: "slug", Usage: "URL slug for the fork"}, {Name: "visibility", Usage: "Public, private or internal"}},
	}.command("forge")

	return []*cobra.Command{pr, check, release, policy, access, fork}
}
