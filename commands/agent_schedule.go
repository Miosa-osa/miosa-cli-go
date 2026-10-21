package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// An agent schedule ('schedule create --agent') is one of the agent's triggers: every
// tick is a run of the agent. The API answers its create with the schedule and the
// trigger, and "run now" goes through the trigger.

var scheduleDetail = []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id"}, {Head: "SCHEDULE", Path: "schedule"}, {Head: "TZ", Path: "timezone"}, {Head: "ON", Compute: machineCell}, {Head: "OVERLAP", Path: "overlap"}, {Head: "TIMEOUT", Path: "timeout_sec"}, {Head: "ENV", Path: "env_names", Fmt: "list"}, {Head: "NEXT", Path: "next_run_at"}}

var agentScheduleDetail = []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id"}, {Head: "AGENT", Path: "agent_id"}, {Head: "SCHEDULE", Path: "schedule"}, {Head: "TZ", Path: "timezone"}, {Head: "TIMEOUT", Path: "timeout_sec"}, {Head: "TASK", Path: "task", Fmt: "clip"}, {Head: "STATE", Path: "state"}}

// resolveAgentSchedule turns --agent and --on into the ids an agent schedule needs.
// A command schedule is left alone: the schedule API resolves its --on itself.
func resolveAgentSchedule(cmd *cobra.Command, c *api.Client) error {
	agent, _ := cmd.Flags().GetString("agent")
	if agent == "" {
		return nil
	}
	id, err := c.ResolveAgent(cmd.Context(), agent)
	if err != nil {
		return die(err)
	}
	_ = cmd.Flags().Set("agent", id)
	if on, _ := cmd.Flags().GetString("on"); on != "" {
		machine, err := resolveMachine(cmd.Context(), c, on)
		if err != nil {
			return die(err)
		}
		_ = cmd.Flags().Set("on", machine)
	}
	return nil
}

// agentScheduleBody is the body of an agent schedule: the agent, its task, and the
// machine it runs on (a fresh one unless --on names one).
func agentScheduleBody(agent string, body map[string]any) (map[string]any, error) {
	delete(body, "agent")
	body["agent_id"] = agent
	if task, _ := body["task"].(string); strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("--task is required with --agent: it is the instruction each run receives")
	}
	if cmd, _ := body["command"].(string); cmd != "" {
		return nil, fmt.Errorf("give --agent or --command, not both")
	}
	for _, key := range []string{"env", "secret", "overlap"} {
		if _, set := body[key]; set {
			return nil, fmt.Errorf("--%s applies to command schedules; an agent run uses the agent's own connections and never overlaps itself", key)
		}
	}
	if t, ok := body["timeout_sec"].(int); ok && (t < 60 || t > 3600) {
		return nil, fmt.Errorf("--timeout must be between 60 and 3600 seconds for an agent schedule")
	}
	if on, _ := body["computer_id"].(string); on != "" {
		delete(body, "computer_id")
		target, id := "sandbox", on
		if rest, ok := strings.CutPrefix(on, computerPrefix); ok {
			target, id = "computer", rest
		} else if rest, ok := strings.CutPrefix(on, sandboxPrefix); ok {
			id = rest
		}
		body["machine"] = map[string]any{"target": target, "id": id}
	}
	return body, nil
}

func printCreatedSchedule(cmd *cobra.Command, args []string, resp any) error {
	m, _ := resp.(map[string]any)
	s, ok := m["schedule"].(map[string]any)
	if !ok {
		return Op{Detail: scheduleDetail}.render(cmd, args, resp)
	}
	if err := (Op{Detail: agentScheduleDetail}).render(cmd, args, s); err != nil {
		return err
	}
	p := printerFor(cmd)
	p.Line("")
	p.Line("Run it now: miosa schedule run %v", s["id"])
	p.Line("Its runs:   miosa agent runs %v", s["agent_id"])
	return nil
}

// runAgentSchedule fires an agent schedule's trigger when --agent is given.
func runAgentSchedule(cmd *cobra.Command, c *api.Client) error {
	agent, _ := cmd.Flags().GetString("agent")
	if agent == "" {
		return nil
	}
	ctx := cmd.Context()
	agentID, err := c.ResolveAgent(ctx, agent)
	if err != nil {
		return die(err)
	}
	ref := cmd.Flags().Arg(0)
	triggerID, err := resolveTrigger(ctx, c, agentID, ref)
	if err != nil {
		return die(err)
	}
	resp, err := c.JSONAny(ctx, api.Request{Method: "POST", Path: "/agents/" + agentID + "/triggers/" + url.PathEscape(triggerID) + "/fire"})
	if err != nil {
		return die(err)
	}
	if err := (Op{}).render(cmd, []string{ref}, resp); err != nil {
		return err
	}
	return errRequestHandled
}

// agentScheduleHint names --agent when the schedule routes do not know a schedule: a
// platform that predates agent schedules on those routes serves them only through the
// agent's trigger.
func agentScheduleHint(_ *cobra.Command, _ *api.Client, args []string, err error) error {
	if api.IsStatus(err, http.StatusNotFound) && len(args) > 0 {
		return withHints(err, fmt.Sprintf("for an agent schedule: miosa schedule run %s --agent <agent>", args[0]))
	}
	return err
}

// resolveTrigger finds one of an agent's triggers by id, short id or name.
func resolveTrigger(ctx context.Context, c *api.Client, agentID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if api.IsUUID(ref) {
		return ref, nil
	}
	if ref == "" {
		return "", errors.New("no schedule specified")
	}
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/agents/"+agentID+"/triggers", nil, &out); err != nil {
		return "", err
	}
	var matches []string
	for _, t := range out.Data {
		if t.Name == ref || (len(ref) >= 4 && strings.HasPrefix(t.ID, ref)) {
			matches = append(matches, t.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("the agent has no schedule %q (see 'miosa agent triggers list %s')", ref, agentID)
	default:
		return "", fmt.Errorf("%q matches %d of the agent's triggers; use the id", ref, len(matches))
	}
}

// resolveTriggerArgs resolves <agent> <trigger> to their ids.
func resolveTriggerArgs(ctx context.Context, c *api.Client, args []string) ([]string, error) {
	agentID, err := c.ResolveAgent(ctx, args[0])
	if err != nil {
		return nil, err
	}
	triggerID, err := resolveTrigger(ctx, c, agentID, args[1])
	if err != nil {
		return nil, err
	}
	return append([]string{agentID, triggerID}, args[2:]...), nil
}

// triggerState is a trigger's enabled flag as a person reads it.
func triggerState(row map[string]any) string {
	if enabled, ok := row["enabled"].(bool); ok && !enabled {
		return "paused"
	}
	return "active"
}
