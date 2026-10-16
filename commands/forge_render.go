package commands

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/spf13/cobra"
)

// stripAvatars removes every "avatar_url" from a decoded response. Profile
// avatars (a Google one is ~2 KB of URL) carry nothing a terminal or a script
// can use, so the CLI never prints them, in text or in --json.
func stripAvatars(v any) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "avatar_url")
		for k, e := range t {
			t[k] = stripAvatars(e)
		}
	case []any:
		for i, e := range t {
			t[i] = stripAvatars(e)
		}
	}
	return v
}

// policySummary says what a merge into the policy's branch needs, in words.
func policySummary(policy map[string]any) string {
	if policy == nil {
		return "no policy"
	}
	approvals, _ := toFloat(policy["required_approvals"])
	independent, _ := policy["require_independent_approval"].(bool)
	selfMerge, _ := policy["self_merge_allowed"].(bool)
	var parts []string
	switch {
	case selfMerge:
		parts = append(parts, "no approval needed (solo organization; the default policy tightens when a second member joins)")
	case approvals == 0:
		parts = append(parts, "no approval needed")
	case independent:
		parts = append(parts, fmt.Sprintf("%s from someone other than the author", plural(int(approvals), "approval")))
	default:
		parts = append(parts, plural(int(approvals), "approval"))
	}
	if checks := stringList(policy["required_checks"]); len(checks) > 0 {
		parts = append(parts, "checks "+strings.Join(checks, ", ")+" must pass")
	}
	if owners, _ := policy["require_code_owner_approval"].(bool); owners {
		parts = append(parts, "a code owner must approve")
	}
	return strings.Join(parts, "; ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func policyName(policy map[string]any) string {
	name := fmt.Sprintf("%v v%v", policy["branch"], formatValue(policy["version"], ""))
	if managed, _ := policy["default_managed"].(bool); managed {
		name += " (default policy)"
	}
	return name
}

// renderPolicy is the text form of a branch policy (policy show, policy set).
func renderPolicy(cmd *cobra.Command, _ []string, resp any) error {
	policy, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	if policy == nil {
		return p.JSON(resp)
	}
	checks := strings.Join(stringList(policy["required_checks"]), ", ")
	if checks == "" {
		checks = "none"
	}
	p.Fields([][2]string{
		{"Policy", policyName(policy)},
		{"Merging needs", policySummary(policy)},
		{"Approvals", formatValue(policy["required_approvals"], "")},
		{"Independent", flagYesNo(policy["require_independent_approval"])},
		{"Self-merge", flagYesNo(policy["self_merge_allowed"])},
		{"Checks", checks},
		{"Code owners", flagYesNo(policy["require_code_owner_approval"])},
		{"Dismiss stale", flagYesNo(policy["dismiss_stale_reviews"])},
	})
	return nil
}

func flagYesNo(v any) string {
	b, _ := v.(bool)
	return yesNo(b)
}

// renderPRSnapshot is the text form of 'forge pr show': state, the policy it
// is judged by, its reviews and checks, and whether it has merged.
func renderPRSnapshot(cmd *cobra.Command, args []string, resp any) error {
	snap, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	if snap == nil {
		return p.JSON(resp)
	}
	policy, _ := snap["policy"].(map[string]any)
	reviews, _ := snap["reviews"].([]any)
	approved := 0
	for _, r := range reviews {
		rv, _ := r.(map[string]any)
		if rv["decision"] == "approved" && rv["dismissed_at"] == nil {
			approved++
		}
	}
	fields := [][2]string{
		{"Pull request", "#" + args[1] + " in " + args[0]},
		{"State", formatValue(snap["state"], "")},
		{"Head", formatValue(snap["head_sha"], "sha")},
	}
	if policy != nil {
		fields = append(fields, [2]string{"Policy", policyName(policy)}, [2]string{"Merging needs", policySummary(policy)})
		need, _ := toFloat(policy["required_approvals"])
		if self, _ := policy["self_merge_allowed"].(bool); self {
			need = 0
		}
		fields = append(fields, [2]string{"Approvals", fmt.Sprintf("%d of %d", approved, int(need))})
	}
	if receipt, ok := snap["merge_receipt"].(map[string]any); ok {
		fields = append(fields, [2]string{"Merged", formatValue(receipt["merge_sha"], "sha") + " into " + strings.TrimPrefix(formatValue(receipt["ref"], ""), "refs/heads/")})
	}
	p.Fields(fields)
	if len(reviews) > 0 {
		rows := make([][]string, 0, len(reviews))
		for _, r := range reviews {
			rv, _ := r.(map[string]any)
			state := formatValue(rv["decision"], "")
			if rv["dismissed_at"] != nil {
				state += " (dismissed)"
			}
			rows = append(rows, []string{reviewerName(rv), state, formatValue(rv["head_sha"], "sha"), clipText(formatValue(rv["body"], ""), 60)})
		}
		p.Line("")
		p.Table([]string{"REVIEWER", "REVIEW", "AT", "COMMENT"}, rows)
	}
	var checks []any
	for _, key := range []string{"check_runs", "commit_checks"} {
		if l, ok := snap[key].([]any); ok {
			checks = append(checks, l...)
		}
	}
	if len(checks) > 0 {
		rows := make([][]string, 0, len(checks))
		for _, c := range checks {
			cr, _ := c.(map[string]any)
			result := formatValue(cr["conclusion"], "")
			if result == "" {
				result = formatValue(cr["status"], "")
			}
			rows = append(rows, []string{formatValue(cr["name"], ""), result, clipText(formatValue(cr["summary"], ""), 60)})
		}
		p.Line("")
		p.Table([]string{"CHECK", "RESULT", "SUMMARY"}, rows)
	}
	return nil
}

func reviewerName(review map[string]any) string {
	if r, ok := review["reviewer"].(map[string]any); ok {
		for _, k := range []string{"display_name", "name"} {
			if s, _ := r[k].(string); s != "" {
				return s
			}
		}
	}
	return formatValue(review["reviewer_user_id"], "short")
}

// renderMergeReceipt is the text form of a merge.
func renderMergeReceipt(cmd *cobra.Command, args []string, resp any) error {
	receipt, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	if receipt == nil {
		return p.JSON(resp)
	}
	if receipt["merge_sha"] == nil && receipt["auto_merge"] == true {
		p.Success("Auto-merge is on: pull request #%s merges by itself (%s) once its branch policy is met", args[1], orDefault(formatValue(receipt["merge_method"], ""), "merge"))
		if w := formatValue(receipt["waiting_for"], ""); w != "" {
			p.Line("Waiting for: %s", strings.ToLower(strings.ReplaceAll(w, "_", " ")))
		}
		return nil
	}
	ref := strings.TrimPrefix(formatValue(receipt["ref"], ""), "refs/heads/")
	p.Success("Merged pull request #%s into %s as %s", args[1], ref, formatValue(receipt["merge_sha"], "sha"))
	p.Fields(nonEmpty([][2]string{
		{"Method", formatValue(receipt["method"], "")},
		{"Policy", "v" + formatValue(receipt["policy_version"], "")},
		{"Base", formatValue(receipt["base_sha"], "sha")},
		{"Head", formatValue(receipt["head_sha"], "sha")},
	}))
	return nil
}

func nonEmpty(rows [][2]string) [][2]string {
	out := rows[:0]
	for _, r := range rows {
		if r[1] != "" && r[1] != "v" {
			out = append(out, r)
		}
	}
	return out
}

// explainMergeRefusal turns a merge the branch policy refused into a sentence
// that names the policy, plus the commands that get past it. A solo
// organization cannot get an approval from someone else, so the policy change
// is printed exactly, not described.
func explainMergeRefusal(cmd *cobra.Command, c *api.Client, args []string, err error) error {
	ae, ok := asAPIError(err)
	if !ok || ae.Status != 422 && ae.Status != 409 {
		return err
	}
	repo, number := args[0], args[1]
	var snap struct {
		Data map[string]any `json:"data"`
	}
	path := "/forge/repositories/" + url.PathEscape(repo) + "/pull-requests/" + url.PathEscape(number) + "/collaboration"
	if c.Get(cmd.Context(), path, nil, &snap) != nil {
		snap.Data = nil
	}
	policy, _ := snap.Data["policy"].(map[string]any)
	branch := "main"
	if policy != nil {
		if b, _ := policy["branch"].(string); b != "" {
			branch = b
		}
	}
	named := "the branch policy"
	if policy != nil {
		named = "policy " + policyName(policy)
	}
	relax := fmt.Sprintf("miosa forge policy set %s --branch %s --approvals 0 --independent=false", repo, branch)
	review := fmt.Sprintf("miosa forge pr review %s %s --decision approved", repo, number)
	out := *ae
	switch strings.ToUpper(ae.Code) {
	case "REQUIRED_APPROVALS_MISSING", "SELF_APPROVAL_FORBIDDEN":
		out.Message = fmt.Sprintf("pull request #%s cannot merge: %s needs %s", number, named, policySummary(policy))
		return withHints(&out,
			"Ask someone with write access to approve:  "+review,
			"Working alone? Let the author merge:      "+relax)
	case "CHANGES_REQUESTED":
		out.Message = fmt.Sprintf("pull request #%s cannot merge: a reviewer requested changes", number)
		return withHints(&out, "Push the changes, then ask the reviewer to approve:  "+review)
	case "REQUIRED_CHECK_NOT_PASSING", "REQUIRED_CHECK_EVIDENCE_UNAVAILABLE", "STALE_CHECK":
		out.Message = fmt.Sprintf("pull request #%s cannot merge: %s needs %s", number, named, policySummary(policy))
		return withHints(&out,
			fmt.Sprintf("See which check is missing:  miosa forge pr show %s %s", repo, number),
			fmt.Sprintf("Change the required checks: miosa forge policy set %s --branch %s --check <name>", repo, branch))
	case "CODE_OWNER_APPROVAL_MISSING":
		out.Message = fmt.Sprintf("pull request #%s cannot merge: %s needs a code owner's approval", number, named)
		return withHints(&out, fmt.Sprintf("See the code owners: miosa forge policy owners %s", repo))
	case "BASE_MOVED", "HEAD_MOVED":
		out.Message = fmt.Sprintf("pull request #%s changed under the merge (%s); its checks and approvals are judged again", number, strings.ToLower(strings.ReplaceAll(ae.Code, "_", " ")))
		return withHints(&out, fmt.Sprintf("Look again, then retry:  miosa forge pr show %s %s", repo, number))
	case "MERGE_CONFLICT":
		out.Message = fmt.Sprintf("pull request #%s conflicts with %s", number, branch)
		return withHints(&out, fmt.Sprintf("Merge %s into your branch locally, push, and retry:  miosa forge pr checkout %s %s", branch, repo, number))
	}
	return err
}

// pickMergeMethod turns --merge, --squash or --rebase into the merge_method the
// API takes; none of them sends no method, and the server merges with a commit.
func pickMergeMethod(cmd *cobra.Command, c *api.Client) error {
	var picked []string
	for _, m := range []string{"merge", "squash", "rebase"} {
		if on, _ := cmd.Flags().GetBool(m); on {
			picked = append(picked, m)
		}
	}
	if len(picked) > 1 {
		return usagef("pick one of --merge, --squash and --rebase (got --%s)", strings.Join(picked, " --"))
	}
	auto, _ := cmd.Flags().GetBool("auto")
	if (len(picked) == 1 && picked[0] != "merge") || auto {
		if err := requireMergeSupport(cmd, c, picked, auto); err != nil {
			return err
		}
	}
	if len(picked) == 1 {
		return cmd.Flags().Set("method", picked[0])
	}
	return nil
}

// requireMergeSupport refuses a merge method or --auto the server does not
// advertise: an older server ignores both and merges with a merge commit.
func requireMergeSupport(cmd *cobra.Command, c *api.Client, picked []string, auto bool) error {
	var caps struct {
		Data struct {
			MergeMethods []string        `json:"merge_methods"`
			Features     map[string]bool `json:"features"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/forge/capabilities", nil, &caps); err != nil {
		return die(fmt.Errorf("checking the server's merge methods: %w", err))
	}
	for _, m := range picked {
		if !contains(caps.Data.MergeMethods, m) {
			return usagef("this MIOSA server does not support --%s yet; nothing was merged", m)
		}
	}
	if auto && !caps.Data.Features["auto_merge"] {
		return usagef("this MIOSA server does not support --auto yet; nothing was merged")
	}
	return nil
}

// renderRelease is the text form of 'forge release show': the release and its
// assets. Asset storage locations are never shown.
func renderRelease(cmd *cobra.Command, _ []string, resp any) error {
	rel, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	if rel == nil {
		return p.JSON(resp)
	}
	p.Fields(nonEmpty([][2]string{
		{"Tag", formatValue(rel["tag"], "")},
		{"Name", formatValue(rel["name"], "")},
		{"Commit", formatValue(rel["commit_sha"], "sha")},
		{"Prerelease", flagYesNo(rel["prerelease"])},
		{"Notes", formatValue(rel["body"], "")},
	}))
	assets, _ := rel["assets"].([]any)
	if len(assets) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(assets))
	for _, a := range assets {
		asset, _ := a.(map[string]any)
		rows = append(rows, []string{formatValue(asset["name"], ""), formatValue(asset["kind"], ""), formatValue(asset["byte_size"], "bytes"), formatValue(asset["sha256"], "sha"), formatValue(asset["id"], "")})
	}
	p.Line("")
	p.Table([]string{"ASSET", "KIND", "SIZE", "SHA256", "ID"}, rows)
	return nil
}
