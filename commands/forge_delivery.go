package commands

// Forge Delivery from the terminal: Missions, phases, Work Orders, the crew of agents that work
// them, and the person steering. Every command is one call to the Delivery service
// (FORGE_LOCAL_URL, default http://127.0.0.1:4020) for the current organization. The default is the
// local service of a Delivery operator; for an account with no Delivery service every command fails
// with one clear message (errDeliveryUnavailable), never a connection error to localhost.
//
// The commands keep their own argument parsing (DisableFlagParsing): flags may come before or after
// the words, `--option` repeats, and `miosa forge check run WO-12 -- <command…>` passes everything
// after `--` through untouched. Agents and the crew prompts rely on exactly this shape.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const forgeDeliveryGroup = "delivery"

// Flags that take no value; every other --flag takes the word after it.
var deliveryBoolFlags = map[string]bool{"mine": true, "ready": true, "standing": true, "full": true, "new": true, "organization": true, "backlog": true, "all": true, "wait": true}

// deliveryArgs is one command's words and flags, parsed the way the crew prompts write them.
type deliveryArgs struct {
	argv       []string
	positional []string
}

func parseDeliveryArgs(argv []string) deliveryArgs {
	a := deliveryArgs{argv: argv}
	for i := 0; i < len(argv); i++ {
		word := argv[i]
		if word == "--" {
			break
		}
		if strings.HasPrefix(word, "--") {
			if !deliveryBoolFlags[strings.TrimPrefix(word, "--")] {
				i++
			}
			continue
		}
		a.positional = append(a.positional, word)
	}
	return a
}

func (a deliveryArgs) flag(name string) string {
	for i, word := range a.argv {
		if word == "--" {
			break
		}
		if word == "--"+name && i+1 < len(a.argv) {
			return a.argv[i+1]
		}
	}
	return ""
}

func (a deliveryArgs) has(name string) bool {
	for _, word := range a.argv {
		if word == "--" {
			break
		}
		if word == "--"+name {
			return true
		}
	}
	return false
}

// repeated is every value of a repeatable flag, in order.
func (a deliveryArgs) repeated(name string) []string {
	var values []string
	for i, word := range a.argv {
		if word == "--" {
			break
		}
		if word == "--"+name && i+1 < len(a.argv) {
			values = append(values, a.argv[i+1])
		}
	}
	return values
}

func (a deliveryArgs) arg(i int) string {
	if i < len(a.positional) {
		return a.positional[i]
	}
	return ""
}

func (a deliveryArgs) rest(from int) []string {
	if from < len(a.positional) {
		return a.positional[from:]
	}
	return nil
}

// omitted is a payload value the request leaves out, so the service applies its own default
// (sending null instead is refused by actions that edit only the fields they are given).
type omittedValue struct{}

var omitted = omittedValue{}

func optional(value string) any {
	if value == "" {
		return omitted
	}
	return value
}

func woNumber(value string) any {
	if value == "" {
		return omitted
	}
	trimmed := regexp.MustCompile(`(?i)^wo-?`).ReplaceAllString(value, "")
	if n, err := strconv.Atoi(trimmed); err == nil {
		return n
	}
	return nil
}

func woLabel(id int) string { return fmt.Sprintf("WO-%d", id) }

func woLabels(ids []int) string {
	labels := make([]string, len(ids))
	for i, id := range ids {
		labels[i] = woLabel(id)
	}
	return strings.Join(labels, ", ")
}

// actor is who is recording this: --as, then FORGE_ACTOR, then "you".
func (a deliveryArgs) actor() string {
	return firstNonEmpty(a.flag("as"), os.Getenv("FORGE_ACTOR"), "you")
}

// runner is the running agent this terminal is (set by `miosa forge join`).
func (a deliveryArgs) runner() string {
	return firstNonEmpty(a.flag("runner"), os.Getenv("FORGE_RUNNER"))
}

// ---- the Delivery service ----

// deliveryDefaultURL is where Delivery is looked for when FORGE_LOCAL_URL is not set.
var deliveryDefaultURL = "http://127.0.0.1:4020"

// errDeliveryUnavailable is what an account without a Delivery service sees, from every command.
var errDeliveryUnavailable = errors.New("Forge Delivery isn't available for this account yet")

type deliveryClient struct {
	// explicit is true when FORGE_LOCAL_URL names the service: only then are connection details
	// worth showing. The default URL is local, so a failure there means "no Delivery here".
	explicit bool
	base     string
	org      string
	out      io.Writer
	http     *http.Client
}

func newDeliveryClient(a deliveryArgs, out io.Writer) *deliveryClient {
	// FORGE_ORG names the Delivery directly; otherwise the organization this CLI works in.
	org := firstNonEmpty(a.flag("org"), os.Getenv("FORGE_ORG"), effectiveOrgFlag())
	return &deliveryClient{
		explicit: os.Getenv("FORGE_LOCAL_URL") != "",
		base:     strings.TrimRight(firstNonEmpty(os.Getenv("FORGE_LOCAL_URL"), deliveryDefaultURL), "/"),
		org:      org,
		out:      out,
		// Not the platform API: no MIOSA org headers, and no "org: …, workspace: …" line.
		http: &http.Client{Timeout: 30 * time.Second, Transport: baseTransport},
	}
}

func (c *deliveryClient) url(path string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	if c.org != "" {
		query.Set("org", c.org)
	}
	if encoded := query.Encode(); encoded != "" {
		return c.base + path + "?" + encoded
	}
	return c.base + path
}

func (c *deliveryClient) unreachable() error {
	if !c.explicit {
		return errDeliveryUnavailable
	}
	return fmt.Errorf("Forge Delivery isn't running at %s. Start it: node tools/forge-local/server.mjs (in MiosaOS)", c.base)
}

// call runs one Delivery action and decodes its result into out (nil to ignore it).
func (c *deliveryClient) call(action string, payload map[string]any, out any) error {
	for key, value := range payload {
		if value == omitted {
			delete(payload, key)
		}
	}
	body, _ := json.Marshal(payload)
	response, err := c.http.Post(c.url("/api/"+action, nil), "application/json", bytes.NewReader(body))
	if err != nil {
		return c.unreachable()
	}
	defer response.Body.Close()
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		if !c.explicit {
			return errDeliveryUnavailable
		}
		return fmt.Errorf("Forge Delivery answered %s with something that isn't JSON", response.Status)
	}
	if !envelope.OK {
		return errors.New(firstNonEmpty(envelope.Error, response.Status))
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func (c *deliveryClient) get(path string, query url.Values, out any) error {
	response, err := c.http.Get(c.url(path, query))
	if err != nil {
		return c.unreachable()
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(response.Body).Decode(&failure)
		if failure.Error == "" && !c.explicit {
			return errDeliveryUnavailable
		}
		return errors.New(firstNonEmpty(failure.Error, response.Status))
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func (c *deliveryClient) state() (*deliveryState, error) {
	var s deliveryState
	if err := c.get("/api/state", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ---- the board, as much of it as the commands read ----

type deliveryText struct {
	Text string `json:"text"`
}

type deliveryState struct {
	Missions   []deliveryMission   `json:"missions"`
	Milestones []deliveryMilestone `json:"milestones"`
	Orders     []deliveryOrder     `json:"orders"`
	Crew       []deliveryRole      `json:"crew"`
	Runners    []deliveryRunner    `json:"runners"`
	Decisions  []deliveryDecision  `json:"decisions"`
	Releases   []deliveryRelease   `json:"releases"`
	Intake     []deliveryNote      `json:"intake"`
	Charter    *deliveryText       `json:"charter"`
	Activity   []deliveryActivity  `json:"activity"`
}

type deliveryMission struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Status   string        `json:"status"`
	Goal     string        `json:"goal"`
	DoneWhen []string      `json:"doneWhen"`
	Brief    *deliveryText `json:"brief"`
}

type deliveryMilestone struct {
	ID       string        `json:"id"`
	Mission  string        `json:"mission"`
	Title    string        `json:"title"`
	DoneWhen string        `json:"doneWhen"`
	Brief    *deliveryText `json:"brief"`
	Verified any           `json:"verified"`
}

type deliverySpec struct {
	Approach         string   `json:"approach"`
	ReadFirst        []string `json:"readFirst"`
	CurrentState     string   `json:"currentState"`
	Trace            string   `json:"trace"`
	Files            []string `json:"files"`
	DoNotTouch       []string `json:"doNotTouch"`
	ReferencePattern string   `json:"referencePattern"`
	Verify           []string `json:"verify"`
	Tests            []string `json:"tests"`
	Risks            []string `json:"risks"`
	SubAgents        []string `json:"subAgents"`
	Estimate         string   `json:"estimate"`
	Source           string   `json:"source"`
}

type deliveryOrder struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	Owner     string `json:"owner"`
	Priority  string `json:"priority"`
	Source    string `json:"source"`
	InMission bool   `json:"inMission"`
	Mission   string `json:"mission"`
	Milestone string `json:"milestone"`
	FoundBy   *int   `json:"foundBy"`
	DoneWhen  string `json:"doneWhen"`
	Territory string `json:"territory"`
	State     string `json:"state"`
	BlockedBy []int  `json:"blockedBy"`
	Steps     []struct {
		Title string `json:"title"`
		Done  bool   `json:"done"`
	} `json:"steps"`
	Checks   string `json:"checks"`
	Approval *struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	} `json:"approval"`
	Branch    *string       `json:"branch"`
	Question  *string       `json:"question"`
	Dismissed bool          `json:"dismissed"`
	Log       []deliveryLog `json:"log"`
	Spec      *deliverySpec `json:"spec"`
	SpecState *string       `json:"specState"`
}

type deliveryLog struct {
	At    int64  `json:"at"`
	Actor string `json:"actor"`
	Text  string `json:"text"`
}

type deliveryRole struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Territory string `json:"territory"`
	Queue     []int  `json:"queue"`
	Paused    bool   `json:"paused"`
	Session   any    `json:"session"`
}

type deliveryRunner struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	Harness   string  `json:"harness"`
	Machine   string  `json:"machine"`
	Sandbox   *string `json:"sandbox"`
	WorkOrder *int    `json:"workOrder"`
	Helping   bool    `json:"helping"`
	Step      any     `json:"step"`
	Prompt    *struct {
		Profile string `json:"profile"`
		Version int    `json:"version"`
	} `json:"prompt"`
}

type deliveryDecision struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Recommended int      `json:"recommended"`
	Chosen      any      `json:"chosen"`
}

type deliveryRelease struct {
	Version    string `json:"version"`
	Stage      string `json:"stage"`
	WorkOrders []int  `json:"workOrders"`
}

type deliveryNote struct {
	ID        string `json:"id"`
	At        int64  `json:"at"`
	By        string `json:"by"`
	Text      string `json:"text"`
	PlannedAt any    `json:"plannedAt"`
	Files     []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"files"`
}

type deliveryActivity struct {
	ID    int    `json:"id"`
	At    int64  `json:"at"`
	Actor string `json:"actor"`
	Text  string `json:"text"`
}

func (s *deliveryState) activeMission() *deliveryMission {
	for i := range s.Missions {
		if s.Missions[i].Status == "active" {
			return &s.Missions[i]
		}
	}
	return nil
}

func (s *deliveryState) mission(id string) *deliveryMission {
	for i := range s.Missions {
		if s.Missions[i].ID == id {
			return &s.Missions[i]
		}
	}
	return nil
}

func (s *deliveryState) milestone(id string) *deliveryMilestone {
	for i := range s.Milestones {
		if s.Milestones[i].ID == id {
			return &s.Milestones[i]
		}
	}
	return nil
}

func (s *deliveryState) order(ref string) *deliveryOrder {
	id, _ := woNumber(ref).(int)
	for i := range s.Orders {
		if s.Orders[i].ID == id {
			return &s.Orders[i]
		}
	}
	return nil
}

func (s *deliveryState) ordersIn(milestone string) []deliveryOrder {
	var list []deliveryOrder
	for _, order := range s.Orders {
		if order.Milestone == milestone && !order.Dismissed {
			list = append(list, order)
		}
	}
	return list
}

// ---- output ----

type deliveryPalette struct{ dim, bold, green, red, yellow, violet, reset string }

func deliveryColors(out io.Writer) deliveryPalette {
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) && os.Getenv("NO_COLOR") == "" {
		return deliveryPalette{"\x1b[2m", "\x1b[1m", "\x1b[32m", "\x1b[31m", "\x1b[33m", "\x1b[35m", "\x1b[0m"}
	}
	return deliveryPalette{}
}

var deliveryStateOrder = []string{"in_progress", "in_review", "merged", "on_staging", "todo", "parked", "shipped"}
var deliveryStateLabel = map[string]string{"todo": "To do", "in_progress": "In progress", "in_review": "In review", "merged": "Merged", "on_staging": "On staging", "shipped": "Shipped", "parked": "Parked"}

func clock(ms int64) string { return time.UnixMilli(ms).Format("3:04:05 PM") }

func joinLines(prefix string, items []string) string {
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = prefix + item
	}
	return strings.Join(lines, "\n")
}

func readSource(source string, stdin io.Reader) ([]byte, error) {
	if source == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(source)
}

func gitOutput(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ---- the commands ----

type deliveryCommand struct {
	use   string
	short string
	run   func(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error
}

func say(c *deliveryClient, format string, values ...any) {
	fmt.Fprintf(c.out, format+"\n", values...)
}

func deliveryCommands() []deliveryCommand {
	commands := []deliveryCommand{
		{"prime", "What any agent harness reads first: the rules and where things stand", deliveryPrime},
		{"status", "The active Mission, its phases, and who is on what", deliveryStatus},
		{"ls", "Work Orders by status", deliveryList},
		{"inbox", "What needs you: decisions, questions, approvals, promotions", deliveryInbox},
		{"show WO-12", "One Work Order: spec, steps, activity", deliveryShow},
		{"context WO-12", "What to read before working: Mission, phase, Work Order [--full]", deliveryContext},
		{"charter", "The Charter above every Mission: how MIOSA ships, what comes first", deliveryCharter},
		{"brief", "The Mission's brief, or a phase's", deliveryBrief},
		{"intake", "Notes left in Forge waiting to be planned", deliveryIntake},
		{"planned <note> WO-12…", "Link a note to the Work Orders it became (or --reason \"why none\")", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("planned", map[string]any{"id": a.arg(0), "workOrders": a.rest(1), "reason": a.flag("reason"), "by": a.actor()}, nil)
		}},
		{"add \"text\"", "Add work: split into Work Orders, deduplicated, queued", deliveryAdd},
		{"spec WO-12", "Write a Work Order's spec, a draft for the person to approve", deliverySpecCmd},
		{"mission new|start|close|edit", "Create, start, close or edit a Mission", deliveryMissionCmd},
		{"edit WO-12", "Edit a Work Order", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("editOrder", map[string]any{"id": woNumber(a.arg(0)), "title": optional(a.flag("title")), "detail": optional(a.flag("detail")), "doneWhen": optional(a.flag("done-when")), "milestone": optional(a.flag("milestone")), "owner": optional(a.flag("owner")), "by": a.actor()}, nil)
		}},
		{"milestone add|edit|branch|merged", "Phases: add one, edit it, give it a branch, record that branch's merge", deliveryMilestoneCmd},
		{"propose \"question\"", "Propose a decision, with your recommendation", deliveryPropose},
		{"autonomy <minutes>", "How long agents wait for you before going with their own call", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			var result struct {
				Minutes any `json:"minutes"`
			}
			if err := c.call("setAutonomy", map[string]any{"minutes": a.arg(0)}, &result); err != nil {
				return err
			}
			say(c, "Agents go ahead on their own call after %v min without a reply.", result.Minutes)
			return nil
		}},
		{"verify <milestone> \"evidence\"", "Sign off a phase: its Done when, checked against real evidence", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if err := c.call("verify", map[string]any{"milestone": a.arg(0), "evidence": strings.Join(a.rest(1), " "), "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "Checkpoint %s verified.", a.arg(0))
			return nil
		}},
		{"join", "Join a Mission's crew as a running agent", deliveryJoin},
		{"take [WO-12]", "Hold a Work Order (the next in your queue if omitted)", deliveryTake},
		{"assist WO-12", "Work one step of a Work Order another agent holds", deliveryAssist},
		{"leave", "Stop this running agent; what it held goes back to the queue", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			runner := a.runner()
			if runner == "" {
				return errors.New("which running agent? --runner <id> or FORGE_RUNNER")
			}
			if err := c.call("leave", map[string]any{"runner": runner}, nil); err != nil {
				return err
			}
			say(c, "%s left. Anything it held is back in the queue.", runner)
			return nil
		}},
		{"steps WO-12 \"a\" \"b\"…", "Plan its steps", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if err := c.call("steps", map[string]any{"id": woNumber(a.arg(0)), "steps": a.rest(1)}, nil); err != nil {
				return err
			}
			say(c, "Planned %d steps.", len(a.rest(1)))
			return nil
		}},
		{"step WO-12 \"a\" | 2 | 2 start", "Mark a step done, or in progress, by title or number", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("step", map[string]any{"id": woNumber(a.arg(0)), "title": strings.Join(a.rest(1), " "), "by": optional(firstNonEmpty(a.flag("by"), a.runner()))}, nil)
		}},
		{"log WO-12 \"text\"", "A line in its activity", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("log", map[string]any{"id": woNumber(a.arg(0)), "text": strings.Join(a.rest(1), " "), "actor": optional(firstNonEmpty(a.flag("as"), a.runner(), os.Getenv("FORGE_ACTOR")))}, nil)
		}},
		{"ask WO-12 \"question\"", "Ask the person, going ahead with your own call unless they steer", deliveryAsk},
		{"wait WO-12", "Block until the person answers (or your own call applies), then print it", deliveryWait},
		{"push WO-12", "Record the branch and commit you just pushed (read from git)", deliveryPush},
		{"done WO-12", "Hand over with a completion report", deliveryDone},
		{"merged WO-12 <sha|PR>", "Record a real merge", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if a.arg(1) == "" {
				return errors.New("give the merge commit or PR link: miosa forge merged WO-12 <sha|PR link>")
			}
			if err := c.call("merged", map[string]any{"id": woNumber(a.arg(0)), "ref": a.arg(1), "into": optional(a.flag("into")), "by": firstNonEmpty(a.flag("as"), a.runner(), os.Getenv("FORGE_ACTOR"), "you")}, nil); err != nil {
				return err
			}
			say(c, "Recorded the merge of %s.", woLabel(woNumber(a.arg(0)).(int)))
			return nil
		}},
		{"discover \"what\"", "File something you found outside your Work Order", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			var result struct {
				ID int `json:"id"`
			}
			if err := c.call("discover", map[string]any{"agent": firstNonEmpty(a.flag("as"), a.runner(), os.Getenv("FORGE_AGENT"), "orchestrator"), "title": strings.Join(a.positional, " "), "from": woNumber(a.flag("from"))}, &result); err != nil {
				return err
			}
			say(c, "%s filed. The Orchestrator places it in a Mission and phase.", woLabel(result.ID))
			return nil
		}},
		{"discoveries", "Orchestrator: discoveries waiting to be placed", deliveryDiscoveries},
		{"place WO-51", "Place a discovery in a Mission and phase", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			var result struct {
				Where string `json:"where"`
			}
			if err := c.call("placeOrder", map[string]any{"id": woNumber(a.arg(0)), "mission": optional(a.flag("mission")), "milestone": optional(a.flag("milestone")), "owner": optional(a.flag("owner")), "priority": optional(a.flag("priority")), "why": optional(a.flag("why")), "by": firstNonEmpty(a.flag("as"), a.runner(), a.actor())}, &result); err != nil {
				return err
			}
			label := woLabel(woNumber(a.arg(0)).(int))
			say(c, "Placed %s: %s. Draft its spec next: miosa forge spec %s --json <file>", label, result.Where, label)
			return nil
		}},
		{"assign <runner>", "Move an agent to another Mission's crew", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if err := c.call("assign", map[string]any{"runner": a.arg(0), "mission": optional(a.flag("mission")), "why": a.flag("why"), "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "%s is in the %s crew now.", a.arg(0), a.flag("mission"))
			return nil
		}},
		{"seats <mission> <role> <n>", "How many agents of a role a Mission's crew has", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			count, err := strconv.Atoi(a.arg(2))
			if a.arg(0) == "" || a.arg(1) == "" || err != nil {
				return errors.New("usage: miosa forge seats <mission> <role> <count>")
			}
			if err := c.call("setSeats", map[string]any{"mission": a.arg(0), "role": a.arg(1), "count": count, "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "%s: %d %s seat(s).", a.arg(0), count, a.arg(1))
			return nil
		}},
		{"prompt [profile]", "The prompt this agent is pinned to (its Mission's, at its version)", deliveryPrompt},
		{"repin [runner]", "Switch to the latest version of its Mission's prompt", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			id := firstNonEmpty(a.arg(0), a.runner())
			if id == "" {
				return errors.New("usage: miosa forge repin <runner>")
			}
			if err := c.call("repin", map[string]any{"runner": id, "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "%s now uses the latest prompt. Re-read it: miosa forge prompt", id)
			return nil
		}},
		{"hooks install", "In an agent's folder: Claude Code reports its tools and sub-agents to Forge live", deliveryHooksInstall},
		{"hook", "Claude Code hook (called by Claude Code, not by people)", deliveryHook},
		{"watch", "The live feed of everything that happens", deliveryWatch},
		{"approve-spec WO-12 | --milestone id", "Approve draft specs (agents can't start without one)", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			payload := map[string]any{"id": woNumber(a.arg(0))}
			if milestone := a.flag("milestone"); milestone != "" {
				payload = map[string]any{"milestone": milestone}
			}
			var result struct {
				Approved []int `json:"approved"`
			}
			if err := c.call("approveSpec", payload, &result); err != nil {
				return err
			}
			say(c, "Approved %s.", plural(len(result.Approved), "spec"))
			return nil
		}},
		{"decide <decision> <option>", "Choose an option of a decision (0 is the first)", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			option, _ := strconv.Atoi(firstNonEmpty(a.arg(1), "0"))
			return c.call("decide", map[string]any{"id": a.arg(0), "option": option}, nil)
		}},
		{"promote [version|--ready]", "Promote what's on staging", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			var result struct {
				Promoted []string `json:"promoted"`
			}
			if err := c.call("promote", map[string]any{"version": firstNonEmpty(a.arg(0), "ready")}, &result); err != nil {
				return err
			}
			say(c, "Promoted %s", strings.Join(result.Promoted, ", "))
			return nil
		}},
	}
	// You, steering: one line each.
	for _, action := range []string{"answer", "approve", "next", "end", "backlog", "cancel", "accept", "dismiss", "retry", "note"} {
		action := action
		commands = append(commands, deliveryCommand{action + " WO-12 [text]", "Steer a Work Order: " + action, func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call(action, map[string]any{"id": woNumber(a.arg(0)), "text": strings.Join(a.rest(1), " ")}, nil)
		}})
	}
	for _, priority := range []string{"p0", "p1", "p2", "p3"} {
		priority := priority
		commands = append(commands, deliveryCommand{priority + " WO-12", "Set the priority to " + strings.ToUpper(priority), func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("priority", map[string]any{"id": woNumber(a.arg(0)), "priority": strings.ToUpper(priority)}, nil)
		}})
	}
	for _, action := range []string{"pause", "resume", "stop"} {
		action := action
		commands = append(commands, deliveryCommand{action + " <agent>", strings.ToUpper(action[:1]) + action[1:] + " a Crew role's agents", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call(action, map[string]any{"agent": a.arg(0)}, nil)
		}})
	}
	return commands
}

func deliveryCobra(command deliveryCommand) *cobra.Command {
	name := strings.Fields(command.use)[0]
	cmd := &cobra.Command{
		Use:                command.use,
		Short:              command.short,
		GroupID:            forgeDeliveryGroup,
		DisableFlagParsing: true,
		Hidden:             name == "hook",
		Example:            deliveryExamples[name],
		RunE: func(cmd *cobra.Command, argv []string) error {
			for _, word := range argv {
				if word == "--" {
					break
				}
				if word == "--help" || word == "-h" {
					return cmd.Help()
				}
			}
			a := parseDeliveryArgs(argv)
			c := newDeliveryClient(a, cmd.OutOrStdout())
			before := countingWriter{w: c.out}
			c.out = &before
			if err := command.run(c, a, cmd); err != nil {
				return err
			}
			if before.n == 0 && !deliveryReadOnly[name] {
				fmt.Fprintln(cmd.OutOrStdout(), "✓")
			}
			return nil
		},
	}
	// Declared for --help and the docs; the command reads them itself (DisableFlagParsing).
	for _, flag := range deliveryFlags[name] {
		flagName, usage, _ := strings.Cut(flag, "=")
		if deliveryBoolFlags[flagName] {
			cmd.Flags().Bool(flagName, false, usage)
		} else {
			cmd.Flags().String(flagName, "", usage)
		}
	}
	return cmd
}

// Commands that only read (or report on their own) print nothing more; a change prints ✓.
var deliveryReadOnly = map[string]bool{"prime": true, "status": true, "ls": true, "inbox": true, "show": true, "context": true, "charter": true, "brief": true, "intake": true, "discoveries": true, "prompt": true, "watch": true, "wait": true, "hook": true, "log": true}

type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	return c.w.Write(p)
}

// addForgeDeliveryCmds puts Delivery under `miosa forge`. `check` already names commit checks, so
// a Work Order's Check is `miosa forge check run WO-12 -- <command…>`.
func addForgeDeliveryCmds(forge *cobra.Command) {
	forge.AddGroup(&cobra.Group{ID: forgeDeliveryGroup, Title: "Delivery (Missions, Work Orders, crew):"})
	for _, command := range deliveryCommands() {
		forge.AddCommand(deliveryCobra(command))
	}
	for _, sub := range forge.Commands() {
		if sub.Name() == "check" {
			run := deliveryCobra(deliveryCommand{"run WO-12 -- <command…>", "Run a real Check on a Work Order and record the result", deliveryCheckRun})
			run.GroupID = ""
			sub.AddCommand(run)
		}
	}
}

func deliveryPrime(c *deliveryClient, _ deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	var guide deliveryText
	if err := c.get("/api/docs/agents", nil, &guide); err != nil {
		return err
	}
	say(c, "%s", guide.Text)
	mission := s.activeMission()
	active := "none"
	if mission != nil {
		active = fmt.Sprintf("%s (%s)", mission.Title, mission.ID)
	}
	say(c, "## Where things stand\n\nActive Mission: %s", active)
	for _, milestone := range s.Milestones {
		if mission != nil && milestone.Mission != mission.ID {
			continue
		}
		items := s.ordersIn(milestone.ID)
		done := 0
		for _, order := range items {
			if order.State == "merged" || order.State == "on_staging" || order.State == "shipped" {
				done++
			}
		}
		verified := ""
		if milestone.Verified != nil {
			verified = " · verified"
		}
		say(c, "- %s  %s  %d/%d done%s\n    Done when: %s", milestone.ID, milestone.Title, done, len(items), verified, milestone.DoneWhen)
	}
	notes := 0
	for _, note := range s.Intake {
		if note.PlannedAt == nil {
			notes++
		}
	}
	if notes > 0 {
		say(c, "\n%s left in Forge wait to be planned: run miosa forge intake.", plural(notes, "note"))
	}
	drafts, open := 0, 0
	for _, order := range s.Orders {
		if order.SpecState != nil && *order.SpecState == "draft" {
			drafts++
		}
	}
	for _, decision := range s.Decisions {
		if decision.Chosen == nil {
			open++
		}
	}
	crew := make([]string, len(s.Crew))
	for i, role := range s.Crew {
		status := "not connected"
		if role.Session != nil {
			status = "connected"
			if role.Paused {
				status = "paused"
			}
		}
		crew[i] = role.ID + " " + status
	}
	say(c, "\n%d specs wait for approval · %d decisions open · Crew: %s", drafts, open, strings.Join(crew, ", "))
	return nil
}

func deliveryStatus(c *deliveryClient, _ deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	p := deliveryColors(c.out)
	planned, shipped := 0, 0
	for _, order := range s.Orders {
		if order.InMission && order.Source == "planned" && !order.Dismissed {
			planned++
			if order.State == "shipped" {
				shipped++
			}
		}
	}
	title := "No active Mission"
	if mission := s.activeMission(); mission != nil {
		title = mission.Title
	}
	say(c, "%s%s%s  %d/%d shipped", p.bold, title, p.reset, shipped, planned)
	for _, milestone := range s.Milestones {
		items := s.ordersIn(milestone.ID)
		done := 0
		for _, order := range items {
			if order.State == "shipped" || order.State == "on_staging" {
				done++
			}
		}
		say(c, "  %-48s %d/%d", milestone.Title, done, len(items))
	}
	say(c, "\n%sCrew%s", p.bold, p.reset)
	for _, role := range s.Crew {
		var running []deliveryRunner
		for _, runner := range s.Runners {
			if runner.Role == role.ID {
				running = append(running, runner)
			}
		}
		count := p.dim + "none running" + p.reset
		if len(running) > 0 {
			count = fmt.Sprintf("%s%d running%s", p.green, len(running), p.reset)
		}
		paused := ""
		if role.Paused {
			paused = " · paused"
		}
		say(c, "  %-13s %s  %s%d queued%s%s", role.Name, count, p.dim, len(role.Queue), paused, p.reset)
		for _, runner := range running {
			where := runner.Machine
			if runner.Sandbox != nil {
				where = "sandbox " + *runner.Sandbox
			}
			holding := p.dim + "free" + p.reset
			if runner.WorkOrder != nil {
				if order := s.order(strconv.Itoa(*runner.WorkOrder)); order != nil {
					label := woLabel(order.ID)
					if runner.Helping {
						label = fmt.Sprintf("helping %s.%v", label, runner.Step)
					}
					holding = label + " " + order.Title
				}
			}
			say(c, "    %-12s %s · %s  %s", runner.ID, runner.Harness, where, holding)
		}
	}
	return nil
}

func deliveryRow(p deliveryPalette, order deliveryOrder) string {
	var marks []string
	if order.Question != nil {
		marks = append(marks, p.violet+"asked you"+p.reset)
	}
	if order.Approval != nil && order.Approval.State == "pending" {
		marks = append(marks, p.yellow+"needs approval"+p.reset)
	}
	if !order.InMission {
		marks = append(marks, p.dim+"backlog"+p.reset)
	}
	return fmt.Sprintf("  %s  %-7s %s  %s%s%s %s", order.Priority, woLabel(order.ID), order.Title, p.dim, order.Owner, p.reset, strings.Join(marks, " "))
}

func deliveryList(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	p := deliveryColors(c.out)
	owner := a.flag("agent")
	if a.has("mine") {
		owner = os.Getenv("FORGE_AGENT")
	}
	for _, status := range deliveryStateOrder {
		if status == "shipped" && !a.has("all") {
			continue
		}
		var items []deliveryOrder
		for _, order := range s.Orders {
			if !order.Dismissed && order.InMission != a.has("backlog") && (owner == "" || order.Owner == owner) && order.State == status {
				items = append(items, order)
			}
		}
		if len(items) == 0 {
			continue
		}
		say(c, "%s%s%s %s%d%s", p.bold, deliveryStateLabel[status], p.reset, p.dim, len(items), p.reset)
		for _, order := range items {
			say(c, "%s", deliveryRow(p, order))
		}
	}
	return nil
}

func deliveryInbox(c *deliveryClient, _ deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	p := deliveryColors(c.out)
	count := 0
	for _, decision := range s.Decisions {
		if decision.Chosen != nil {
			continue
		}
		count++
		say(c, "%sdecision%s %s: %s", p.violet, p.reset, decision.ID, decision.Question)
		for i, option := range decision.Options {
			recommended := ""
			if i == decision.Recommended {
				recommended = " (recommended)"
			}
			say(c, "   %d) %s%s", i, option, recommended)
		}
	}
	for _, order := range s.Orders {
		if order.Question != nil {
			count++
			say(c, "%squestion%s %s %s: %s   → miosa forge answer %s \"…\"", p.violet, p.reset, woLabel(order.ID), order.Owner, *order.Question, woLabel(order.ID))
		}
	}
	for _, order := range s.Orders {
		if order.Approval != nil && order.Approval.State == "pending" {
			count++
			say(c, "%sapprove%s %s %s (%s)   → miosa forge approve %s", p.yellow, p.reset, woLabel(order.ID), order.Title, order.Approval.Reason, woLabel(order.ID))
		}
	}
	for _, release := range s.Releases {
		if release.Stage == "staging" {
			count++
			say(c, "%spromote%s %s: %s   → miosa forge promote %s", p.green, p.reset, release.Version, woLabels(release.WorkOrders), release.Version)
		}
	}
	for _, order := range s.Orders {
		if order.Source == "discovered" && !order.InMission && !order.Dismissed {
			say(c, "discovery %s %s   (the Orchestrator is placing it; miosa forge dismiss %s to drop it)", woLabel(order.ID), order.Title, woLabel(order.ID))
		}
	}
	if count == 0 {
		say(c, "Nothing needs you.")
	}
	return nil
}

func deliveryShow(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	order := s.order(a.arg(0))
	if order == nil {
		return fmt.Errorf("no Work Order %s", a.arg(0))
	}
	p := deliveryColors(c.out)
	branch := "no branch"
	if order.Branch != nil {
		branch = *order.Branch
	}
	say(c, "%s%s %s%s", p.bold, woLabel(order.ID), order.Title, p.reset)
	say(c, "%s · %s · %s · %s · Checks %s", deliveryStateLabel[order.State], order.Priority, order.Owner, branch, order.Checks)
	if order.Detail != "" {
		say(c, "\n%s", order.Detail)
	}
	say(c, "\nDone when: %s", order.DoneWhen)
	if spec := order.Spec; spec != nil {
		specState := ""
		if order.SpecState != nil {
			specState = *order.SpecState
		}
		say(c, "\n%sSpec%s (%s)\n  %s", p.bold, p.reset, specState, spec.Approach)
		if len(spec.Files) > 0 {
			say(c, "  files: %s", strings.Join(spec.Files, ", "))
		}
		if len(spec.Tests) > 0 {
			say(c, "  tests: %s", strings.Join(spec.Tests, "; "))
		}
		if len(spec.Risks) > 0 {
			say(c, "  risks: %s", strings.Join(spec.Risks, "; "))
		}
		subAgents := firstNonEmpty(strings.Join(spec.SubAgents, ", "), "none")
		blocked := ""
		if len(order.BlockedBy) > 0 {
			blocked = " · blocked by " + woLabels(order.BlockedBy)
		}
		say(c, "  sub-agents: %s · estimate %s%s", subAgents, firstNonEmpty(spec.Estimate, "?"), blocked)
	}
	if len(order.Steps) > 0 {
		lines := make([]string, len(order.Steps))
		for i, step := range order.Steps {
			mark := "○"
			if step.Done {
				mark = "✓"
			}
			lines[i] = fmt.Sprintf("  %s %d. %s", mark, i+1, step.Title)
		}
		say(c, "\nSteps:\n%s", strings.Join(lines, "\n"))
	}
	if order.Question != nil {
		say(c, "\n%sWaiting on you:%s %s", p.violet, p.reset, *order.Question)
	}
	entries := order.Log
	if len(entries) > 12 {
		entries = entries[len(entries)-12:]
	}
	lines := make([]string, len(entries))
	for i, entry := range entries {
		lines[i] = fmt.Sprintf("  %s%s%s %s: %s", p.dim, clock(entry.At), p.reset, entry.Actor, entry.Text)
	}
	say(c, "\nActivity:\n%s", strings.Join(lines, "\n"))
	return nil
}

func deliveryContext(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	order := s.order(a.arg(0))
	if order == nil {
		return fmt.Errorf("no Work Order %s", a.arg(0))
	}
	mission := s.mission(order.Mission)
	if mission == nil {
		mission = s.activeMission()
	}
	if s.Charter != nil && s.Charter.Text != "" {
		say(c, "# 0. Charter\n\nEvery Mission answers to it: miosa forge charter. It wins when anything below disagrees.\n")
	}
	if mission != nil {
		doneWhen := "- (not set)"
		if len(mission.DoneWhen) > 0 {
			doneWhen = joinLines("- ", mission.DoneWhen)
		}
		say(c, "# 1. Mission: %s\n\nGoal: %s\nDone when:\n%s", mission.Title, firstNonEmpty(mission.Goal, "(none)"), doneWhen)
		if mission.Brief != nil && mission.Brief.Text != "" {
			if a.has("full") {
				say(c, "\n%s", mission.Brief.Text)
			} else {
				say(c, "\nThe full brief (%d lines) is the source of truth for this Mission: miosa forge brief", len(strings.Split(mission.Brief.Text, "\n")))
			}
		}
	} else {
		say(c, "# 1. Mission: none\n\nGoal: (none)\nDone when:\n- (not set)")
	}
	if milestone := s.milestone(order.Milestone); milestone != nil {
		brief := ""
		if milestone.Brief != nil && milestone.Brief.Text != "" {
			brief = "\n\n" + milestone.Brief.Text
		}
		say(c, "\n# 2. Phase: %s\n\nDone when: %s%s", milestone.Title, milestone.DoneWhen, brief)
	}
	specState := "none"
	if order.SpecState != nil {
		specState = *order.SpecState
	}
	say(c, "\n# 3. %s: %s\n\nOwner: %s · %s · spec %s\nDone when: %s", woLabel(order.ID), order.Title, order.Owner, order.Priority, specState, order.DoneWhen)
	if order.Detail != "" {
		say(c, "\n%s", order.Detail)
	}
	if spec := order.Spec; spec != nil {
		if len(spec.ReadFirst) > 0 {
			lines := make([]string, len(spec.ReadFirst))
			for i, item := range spec.ReadFirst {
				lines[i] = fmt.Sprintf("%d. %s", i+1, item)
			}
			say(c, "\nRead first, in order:\n%s", strings.Join(lines, "\n"))
		}
		say(c, "\nApproach: %s", spec.Approach)
		if spec.CurrentState != "" {
			say(c, "Current state: %s", spec.CurrentState)
		}
		if spec.Trace != "" {
			say(c, "Trace: %s", spec.Trace)
		}
		if spec.ReferencePattern != "" {
			say(c, "Follow the pattern in: %s", spec.ReferencePattern)
		}
		for _, section := range []struct {
			name  string
			items []string
		}{{"Files you change", spec.Files}, {"Do not touch", spec.DoNotTouch}, {"Prove it with", spec.Verify}, {"Proven by", spec.Tests}, {"Risks", spec.Risks}} {
			if len(section.items) > 0 {
				say(c, "%s:\n%s", section.name, joinLines("- ", section.items))
			}
		}
		extra := ""
		if spec.Estimate != "" {
			extra += " · estimate " + spec.Estimate
		}
		if spec.Source != "" {
			extra += " · source " + spec.Source
		}
		say(c, "Sub-agents: %s%s", firstNonEmpty(strings.Join(spec.SubAgents, ", "), "none"), extra)
	}
	if len(order.Steps) > 0 {
		lines := make([]string, len(order.Steps))
		for i, step := range order.Steps {
			mark := "○"
			if step.Done {
				mark = "✓"
			}
			lines[i] = fmt.Sprintf("%s %s.%d %s", mark, woLabel(order.ID), i+1, step.Title)
		}
		say(c, "\nSteps:\n%s", strings.Join(lines, "\n"))
	}
	if len(order.BlockedBy) > 0 {
		say(c, "\nBlocked by: %s", woLabels(order.BlockedBy))
	}
	territory := order.Territory
	for _, role := range s.Crew {
		if role.ID == order.Owner && role.Territory != "" {
			territory = role.Territory
		}
	}
	never := ""
	if order.Spec != nil && len(order.Spec.DoNotTouch) > 0 {
		never = ", never " + strings.Join(order.Spec.DoNotTouch, ", ")
	}
	label := woLabel(order.ID)
	say(c, `
# 4. How to work it

- Territory: %s. Read anything; change only the spec's files%s.
- Match the codebase: its naming, error handling and patterns. The minimum correct change; nothing the spec doesn't ask for.
- After every step: build and run the tests. A failure gets 3 tries (fix the error, rethink, start fresh); then miosa forge ask with your own call, or the Work Order parks.
- Found something else? miosa forge discover "…" --from %s. Don't fix it.
- Prove it: miosa forge check run %s -- <command>. Then miosa forge done %s --report - with what was wrong, what changed file by file, and the commands and results.`,
		strings.TrimSuffix(territory, "."), never, label, label, label)
	return nil
}

func deliveryCharter(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error {
	if source := a.flag("set"); source != "" {
		text, err := readSource(source, cmd.InOrStdin())
		if err != nil {
			return err
		}
		if err := c.call("editCharter", map[string]any{"text": string(text), "by": a.actor()}, nil); err != nil {
			return err
		}
		say(c, "Charter saved.")
		return nil
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	if s.Charter == nil || s.Charter.Text == "" {
		say(c, "(no Charter yet: miosa forge charter --set charter.md)")
		return nil
	}
	say(c, "%s", s.Charter.Text)
	return nil
}

func deliveryBrief(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	phase := a.flag("phase")
	var mission *deliveryMission
	if id := a.flag("mission"); id != "" {
		mission = s.mission(id)
	} else {
		mission = s.activeMission()
	}
	if mission == nil {
		return errors.New("no active Mission. Pass --mission <id>")
	}
	if source := a.flag("set"); source != "" {
		text, err := readSource(source, cmd.InOrStdin())
		if err != nil {
			return err
		}
		target := "for " + mission.Title
		if phase != "" {
			target = "for " + phase
			err = c.call("editMilestone", map[string]any{"id": phase, "brief": string(text), "by": a.actor()}, nil)
		} else {
			err = c.call("editMission", map[string]any{"id": mission.ID, "brief": string(text), "by": a.actor()}, nil)
		}
		if err != nil {
			return err
		}
		say(c, "Brief %s saved (%d lines).", target, len(strings.Split(string(text), "\n")))
		return nil
	}
	brief := mission.Brief
	if phase != "" {
		milestone := s.milestone(phase)
		if milestone == nil {
			return fmt.Errorf("no phase %s", phase)
		}
		brief = milestone.Brief
	}
	if brief == nil || brief.Text == "" {
		hint := ""
		if phase != "" {
			hint = "--phase " + phase + " "
		}
		say(c, "(no brief yet: miosa forge brief %s--set <file.md>)", hint)
		return nil
	}
	say(c, "%s", brief.Text)
	return nil
}

var dataURL = regexp.MustCompile(`^data:[^;]*;base64,(.*)$`)
var unsafeFileChars = regexp.MustCompile(`[^\w.-]`)

func deliveryIntake(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	var open []deliveryNote
	for i := len(s.Intake) - 1; i >= 0; i-- {
		if s.Intake[i].PlannedAt == nil {
			open = append(open, s.Intake[i])
		}
	}
	if len(open) == 0 {
		say(c, "No notes waiting to be planned.")
		return nil
	}
	p := deliveryColors(c.out)
	dir := firstNonEmpty(a.flag("save"), filepath.Join(os.TempDir(), "forge-intake"))
	for _, note := range open {
		say(c, "%s%s%s %s%s · from %s%s\n%s", p.bold, note.ID, p.reset, p.dim, time.UnixMilli(note.At).Format("Jan 2 3:04 PM"), note.By, p.reset, firstNonEmpty(note.Text, "(no text)"))
		for _, file := range note.Files {
			match := dataURL.FindStringSubmatch(file.URL)
			if match == nil {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(match[1])
			if err != nil {
				continue
			}
			folder := filepath.Join(dir, note.ID)
			if err := os.MkdirAll(folder, 0o755); err != nil {
				return err
			}
			path := filepath.Join(folder, unsafeFileChars.ReplaceAllString(file.Name, "_"))
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
			say(c, "  attached: %s", path)
		}
		say(c, "")
	}
	say(c, `Plan each into Work Orders (miosa forge add … then miosa forge spec …), then: miosa forge planned <note> WO-<n> [WO-<m>…]  or  miosa forge planned <note> --reason "why none"`)
	return nil
}

func deliveryAdd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	var results []struct {
		ID      int    `json:"id"`
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	}
	payload := map[string]any{"text": strings.Join(a.positional, " "), "owner": optional(a.flag("owner")), "priority": optional(a.flag("priority")), "milestone": optional(a.flag("milestone")), "mission": optional(a.flag("mission")), "isNew": a.has("new"), "by": a.actor()}
	if err := c.call("add", payload, &results); err != nil {
		return err
	}
	for _, result := range results {
		arrow := "→"
		if result.Kind == "duplicate" {
			arrow = "duplicate:"
		}
		say(c, "%s %s %s", woLabel(result.ID), arrow, result.Summary)
	}
	return nil
}

func deliverySpecCmd(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error {
	source := a.flag("json")
	if source == "" {
		return errors.New("give the spec as JSON: miosa forge spec WO-12 --json spec.json (or - for stdin)")
	}
	raw, err := readSource(source, cmd.InOrStdin())
	if err != nil {
		return err
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("the spec isn't valid JSON: %w", err)
	}
	payload := map[string]any{"id": woNumber(a.arg(0)), "by": a.actor(), "source": firstNonEmpty(fmt.Sprint(spec["source"]), "")}
	if spec["source"] == nil {
		payload["source"] = ""
	}
	for _, key := range []string{"owner", "priority", "doneWhen", "blockedBy"} {
		payload[key] = spec[key]
		delete(spec, key)
	}
	payload["spec"] = spec
	if err := c.call("spec", payload, nil); err != nil {
		return err
	}
	label := woLabel(woNumber(a.arg(0)).(int))
	say(c, "Draft spec on %s. It waits for approval: miosa forge approve-spec %s", label, label)
	return nil
}

func splitList(value, separator string) []string {
	if value == "" {
		return []string{""}
	}
	return strings.Split(value, separator)
}

func deliveryMissionCmd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	verb, rest := a.arg(0), a.rest(1)
	first := ""
	if len(rest) > 0 {
		first = rest[0]
	}
	switch verb {
	case "new":
		standing := a.has("standing")
		kind := "goal"
		if standing {
			kind = "standing"
		}
		owners := []string{}
		for _, owner := range strings.Split(a.flag("owners"), ",") {
			if owner != "" {
				owners = append(owners, owner)
			}
		}
		var scope any
		if a.has("organization") {
			scope = map[string]any{"kind": "organization"}
		} else if workspace := a.flag("workspace"); workspace != "" {
			workspaceScope := map[string]any{"kind": "workspace", "id": workspace}
			if name := a.flag("workspace-name"); name != "" {
				workspaceScope["name"] = name
			}
			scope = workspaceScope
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := c.call("createMission", map[string]any{"title": strings.Join(rest, " "), "goal": a.flag("goal"), "doneWhen": splitList(a.flag("done-when"), "|"), "kind": kind, "owners": owners, "scope": scope, "by": a.actor()}, &result); err != nil {
			return err
		}
		if standing {
			say(c, "Standing Mission %s: always open. Add work with miosa forge add \"…\" --mission %s", result.ID, result.ID)
		} else {
			say(c, "Mission %s (planned). Add milestones and Work Orders, then: miosa forge mission start %s", result.ID, result.ID)
		}
		return nil
	case "start":
		if err := c.call("startMission", map[string]any{"id": first, "by": a.actor()}, nil); err != nil {
			return err
		}
		say(c, "Started.")
		return nil
	case "close":
		var result struct {
			CarriedForward []int `json:"carriedForward"`
		}
		if err := c.call("closeMission", map[string]any{"id": first, "by": a.actor()}, &result); err != nil {
			return err
		}
		say(c, "Closed. Carried forward: %s.", firstNonEmpty(woLabels(result.CarriedForward), "nothing"))
		return nil
	case "edit":
		var doneWhen any
		if value := a.flag("done-when"); value != "" {
			doneWhen = strings.Split(value, "|")
		}
		return c.call("editMission", map[string]any{"id": first, "title": optional(a.flag("title")), "goal": optional(a.flag("goal")), "doneWhen": doneWhen, "by": a.actor()}, nil)
	}
	return errors.New("miosa forge mission new|start|close|edit …")
}

func deliveryMilestoneCmd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	switch a.arg(0) {
	case "edit":
		return c.call("editMilestone", map[string]any{"id": a.arg(1), "title": optional(a.flag("title")), "doneWhen": optional(a.flag("done-when")), "by": a.actor()}, nil)
	case "branch":
		return c.call("phaseBranch", map[string]any{"id": a.arg(1), "branch": a.arg(2), "into": optional(a.flag("into")), "by": a.actor()}, nil)
	case "merged":
		return c.call("phaseMerged", map[string]any{"id": a.arg(1), "ref": a.arg(2), "into": optional(a.flag("into")), "by": a.actor()}, nil)
	case "add":
		var result struct {
			ID string `json:"id"`
		}
		if err := c.call("addMilestone", map[string]any{"title": strings.Join(a.rest(1), " "), "doneWhen": optional(a.flag("done-when")), "after": optional(a.flag("after")), "by": a.actor()}, &result); err != nil {
			return err
		}
		say(c, "Milestone %s.", result.ID)
		return nil
	}
	return errors.New(`miosa forge milestone add "title" --done-when "…" [--after id] | miosa forge milestone edit <id> [--title] [--done-when]`)
}

func deliveryPropose(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	options := []string{}
	for _, option := range strings.Split(a.flag("options"), "|") {
		if option = strings.TrimSpace(option); option != "" {
			options = append(options, option)
		}
	}
	recommended, _ := strconv.Atoi(firstNonEmpty(a.flag("recommend"), "0"))
	var result struct {
		ID string `json:"id"`
	}
	if err := c.call("propose", map[string]any{"question": strings.Join(a.positional, " "), "context": a.flag("context"), "options": options, "recommended": recommended, "by": a.actor(), "minutes": optional(a.flag("minutes")), "mission": optional(a.flag("mission"))}, &result); err != nil {
		return err
	}
	say(c, "Decision %s.", result.ID)
	return nil
}

func deliveryJoin(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	agent := firstNonEmpty(a.flag("as"), os.Getenv("FORGE_AGENT"))
	if agent == "" {
		return errors.New("say which Crew role: miosa forge join --as frontend --harness claude-code [--mission <id>] [--on local|sandbox:<id>|computer:<id>|machine:<name>]")
	}
	sandbox := a.flag("sandbox")
	kind := "local"
	if sandbox != "" {
		kind = "sandbox"
	}
	var result struct {
		Runner  string `json:"runner"`
		Mission string `json:"mission"`
		Queue   []int  `json:"queue"`
	}
	payload := map[string]any{"agent": agent, "harness": firstNonEmpty(a.flag("harness"), "terminal"), "machine": firstNonEmpty(a.flag("machine"), "this laptop"), "kind": kind, "sandbox": optional(sandbox), "on": optional(a.flag("on")), "mission": optional(a.flag("mission")), "worktree": optional(firstNonEmpty(a.flag("worktree"), gitOutput("rev-parse", "--show-toplevel"))), "specialty": optional(a.flag("specialty"))}
	if err := c.call("join", payload, &result); err != nil {
		return err
	}
	say(c, "You are %s, in the %s crew. %d Work Orders for your role in this Mission.\nSet it for this terminal:  export FORGE_RUNNER=%s\nRead your prompt: miosa forge prompt   Then: miosa forge take", result.Runner, result.Mission, len(result.Queue), result.Runner)
	return nil
}

func deliveryTake(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	runner, agent := a.runner(), firstNonEmpty(a.flag("as"), os.Getenv("FORGE_AGENT"))
	if runner == "" && agent == "" {
		return errors.New("join first (miosa forge join --as backend), then export FORGE_RUNNER=<id> or pass --runner <id>")
	}
	var order struct {
		ID       int     `json:"id"`
		Title    string  `json:"title"`
		Branch   *string `json:"branch"`
		DoneWhen string  `json:"doneWhen"`
	}
	payload := map[string]any{"runner": optional(runner), "agent": optional(agent), "id": woNumber(a.arg(0)), "branch": optional(firstNonEmpty(a.flag("branch"), gitOutput("rev-parse", "--abbrev-ref", "HEAD"))), "worktree": optional(firstNonEmpty(a.flag("worktree"), gitOutput("rev-parse", "--show-toplevel")))}
	if err := c.call("take", payload, &order); err != nil {
		return err
	}
	branch := ""
	if order.Branch != nil && *order.Branch != "" {
		branch = "\nbranch " + *order.Branch
	}
	say(c, "Holding %s: %s%s\ndone when: %s", woLabel(order.ID), order.Title, branch, order.DoneWhen)
	return nil
}

func deliveryAssist(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	if a.arg(0) == "" || a.flag("step") == "" {
		return errors.New("miosa forge assist WO-12 --step 3   (work one step of a Work Order someone else holds)")
	}
	if err := c.call("help", map[string]any{"runner": optional(a.runner()), "agent": optional(firstNonEmpty(a.flag("as"), os.Getenv("FORGE_AGENT"))), "id": woNumber(a.arg(0)), "step": a.flag("step")}, nil); err != nil {
		return err
	}
	label := woLabel(woNumber(a.arg(0)).(int))
	say(c, "Helping on %s.%s. Mark it done with: miosa forge step %s \"<step>\" --by %s", label, a.flag("step"), label, firstNonEmpty(a.runner(), "<you>"))
	return nil
}

func deliveryAsk(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	options := []map[string]string{}
	for _, value := range a.repeated("option") {
		label, detail, _ := strings.Cut(value, "::")
		options = append(options, map[string]string{"label": strings.TrimSpace(label), "detail": strings.TrimSpace(detail)})
	}
	var recommended any
	if value := a.flag("recommend"); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			recommended = n - 1
		}
	}
	var result struct {
		Gated bool `json:"gated"`
	}
	payload := map[string]any{"id": woNumber(a.arg(0)), "question": strings.Join(a.rest(1), " "), "fallback": optional(a.flag("default")), "minutes": optional(a.flag("minutes")), "options": options, "recommended": recommended}
	if err := c.call("ask", payload, &result); err != nil {
		return err
	}
	label := woLabel(woNumber(a.arg(0)).(int))
	if result.Gated {
		say(c, "Asked. This one waits for them: miosa forge wait %s", label)
	} else {
		say(c, "Asked. Going ahead with “%s” unless they steer. miosa forge wait %s returns whichever applies.", a.flag("default"), label)
	}
	return nil
}

func deliveryWait(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	for {
		var result struct {
			Question *string `json:"question"`
			Answer   *string `json:"answer"`
		}
		if err := c.call("pendingAnswer", map[string]any{"id": woNumber(a.arg(0))}, &result); err != nil {
			return err
		}
		if result.Question == nil {
			if result.Answer != nil {
				say(c, "%s", *result.Answer)
			} else {
				say(c, "(no question was open)")
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

func deliveryPush(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	branch := firstNonEmpty(a.flag("branch"), gitOutput("rev-parse", "--abbrev-ref", "HEAD"))
	sha := gitOutput("rev-parse", "HEAD")
	if branch == "" {
		return errors.New("run this inside the repo you pushed, or pass --branch <name>")
	}
	if err := c.call("push", map[string]any{"id": woNumber(a.arg(0)), "branch": branch, "sha": optional(sha)}, nil); err != nil {
		return err
	}
	at := ""
	if len(sha) >= 9 {
		at = " at " + sha[:9]
	}
	say(c, "Recorded %s%s.", branch, at)
	return nil
}

// deliveryCheckRun runs the command after `--` in a shell, streams its output, and records the
// Check (running, then passed or failed with the last 40 lines) on the Work Order.
func deliveryCheckRun(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error {
	split := -1
	for i, word := range a.argv {
		if word == "--" {
			split = i
			break
		}
	}
	if split < 0 || split == len(a.argv)-1 {
		return errors.New("give the command after --: miosa forge check run WO-12 -- pnpm test")
	}
	id := woNumber(a.arg(0))
	line := strings.Join(a.argv[split+1:], " ")
	started := time.Now()
	var run struct {
		ID any `json:"id"`
	}
	if err := c.call("check", map[string]any{"id": id, "command": line, "state": "running", "seconds": 0}, &run); err != nil {
		return err
	}
	tail := &tailWriter{}
	shell := exec.Command("sh", "-c", line)
	shell.Stdin = nil
	shell.Stdout = io.MultiWriter(cmd.OutOrStdout(), tail)
	shell.Stderr = io.MultiWriter(cmd.ErrOrStderr(), tail)
	code := 0
	if err := shell.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		code = exit.ExitCode()
	}
	seconds := int(time.Since(started).Round(time.Second).Seconds())
	result := "failed"
	if code == 0 {
		result = "passed"
	}
	if err := c.call("check", map[string]any{"id": id, "run": run.ID, "command": line, "state": result, "output": tail.last(40), "seconds": seconds}, nil); err != nil {
		return err
	}
	p := deliveryColors(c.out)
	color, verdict := p.green, "Check passed"
	if code != 0 {
		color, verdict = p.red, "Check failed"
	}
	say(c, "\n%s%s%s (%ds) · recorded on %s", color, verdict, p.reset, seconds, woLabel(id.(int)))
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// tailWriter keeps the last lines written to it.
type tailWriter struct {
	lines   []string
	pending string
}

func (t *tailWriter) Write(p []byte) (int, error) {
	parts := strings.Split(t.pending+string(p), "\n")
	t.pending = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		if line != "" {
			t.lines = append(t.lines, line)
		}
	}
	if len(t.lines) > 400 {
		t.lines = t.lines[len(t.lines)-400:]
	}
	return len(p), nil
}

func (t *tailWriter) last(n int) []string {
	lines := append([]string{}, t.lines...)
	if t.pending != "" {
		lines = append(lines, t.pending)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

const deliveryReportShape = `
  {
    "summary": "what was done, what wasn't and why",
    "rootCause": "what was wrong, one sentence",
    "changes": [{ "file": "path", "change": "exactly what changed" }],
    "verification": [{ "command": "the real command", "result": "PASS, 412 tests" }],
    "discoveries": ["anything found outside the spec (also: miosa forge discover)"],
    "forOthers": ["notes for another role"],
    "left": "what's left, if anything"
  }`

func deliveryDone(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error {
	source := a.flag("report")
	if source == "" {
		return fmt.Errorf("hand over with a completion report (JSON): miosa forge done %s --report report.json (or - for stdin)%s", firstNonEmpty(a.arg(0), "WO-12"), deliveryReportShape)
	}
	raw, err := readSource(source, cmd.InOrStdin())
	if err != nil {
		return err
	}
	var report any
	if err := json.Unmarshal(raw, &report); err != nil {
		return fmt.Errorf("the report isn't valid JSON: %w", err)
	}
	var order struct {
		ID       int `json:"id"`
		Approval *struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"approval"`
	}
	if err := c.call("done", map[string]any{"id": woNumber(a.arg(0)), "report": report}, &order); err != nil {
		return err
	}
	if order.Approval != nil && order.Approval.State == "pending" {
		say(c, "Waiting for approval: it %s.", order.Approval.Reason)
	} else {
		say(c, "Handed over %s for review. Once it really merges: miosa forge merged %s <sha|PR link>", woLabel(order.ID), woLabel(order.ID))
	}
	return nil
}

func deliveryDiscoveries(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	unplaced := func(s *deliveryState) []deliveryOrder {
		var list []deliveryOrder
		for _, order := range s.Orders {
			if order.Source == "discovered" && !order.InMission && !order.Dismissed && order.Question == nil {
				list = append(list, order)
			}
		}
		return list
	}
	deadline := time.Now().Add(9 * time.Minute)
	s, err := c.state()
	if err != nil {
		return err
	}
	for a.has("wait") && len(unplaced(s)) == 0 && time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if s, err = c.state(); err != nil {
			return err
		}
	}
	list := unplaced(s)
	if len(list) == 0 {
		say(c, "No discoveries to place.")
		return nil
	}
	for _, order := range list {
		found := ""
		if order.FoundBy != nil {
			found = " · found on " + woLabel(*order.FoundBy)
		}
		say(c, "%s [%s · %s%s] %s", woLabel(order.ID), order.Owner, order.Priority, found, order.Title)
	}
	say(c, "\nPlace each: miosa forge place WO-<n> --milestone <phase> --owner <role> --priority P0-P3 --why \"…\"   (miosa forge context WO-<n> first)")
	return nil
}

func deliveryPrompt(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	profile, version := a.arg(0), 0
	id := a.runner()
	for _, runner := range s.Runners {
		if runner.ID == id && runner.Prompt != nil {
			profile, version = runner.Prompt.Profile, runner.Prompt.Version
		}
	}
	if profile == "" {
		return errors.New("set FORGE_RUNNER (or pass a profile: miosa forge prompt backend)")
	}
	query := url.Values{}
	if version > 0 {
		query.Set("v", strconv.Itoa(version))
	}
	var result struct {
		Name    string `json:"name"`
		Version struct {
			V     int `json:"v"`
			Files []struct {
				Text string `json:"text"`
			} `json:"files"`
		} `json:"version"`
		Versions []struct {
			V int `json:"v"`
		} `json:"versions"`
	}
	if err := c.get("/api/profile/"+url.PathEscape(profile), query, &result); err != nil {
		return err
	}
	newer := ""
	if n := len(result.Versions); n > 0 && result.Versions[n-1].V > result.Version.V {
		newer = fmt.Sprintf(" (v%d exists: miosa forge repin)", result.Versions[n-1].V)
	}
	say(c, "# %s · v%d%s\n", result.Name, result.Version.V, newer)
	for _, file := range result.Version.Files {
		say(c, "%s\n", strings.TrimSpace(file.Text))
	}
	return nil
}

func deliveryWatch(c *deliveryClient, _ deliveryArgs, _ *cobra.Command) error {
	p := deliveryColors(c.out)
	seen := 0
	for {
		s, err := c.state()
		if err != nil {
			return err
		}
		for i := len(s.Activity) - 1; i >= 0; i-- {
			entry := s.Activity[i]
			if entry.ID > seen {
				say(c, "%s%s%s %s%s%s %s", p.dim, clock(entry.At), p.reset, p.bold, entry.Actor, p.reset, entry.Text)
				seen = entry.ID
			}
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

// deliveryHooksInstall writes Claude Code hooks into this folder's .claude/settings.local.json, so a
// running agent's tools and sub-agents show live in Forge. The hook is this binary.
func deliveryHooksInstall(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	if a.arg(0) != "install" {
		return errors.New("usage: miosa forge hooks install   (in the folder the agent's Claude Code runs in)")
	}
	binary, err := os.Executable()
	if err != nil {
		binary = "miosa"
	}
	command := fmt.Sprintf("%q forge hook", binary)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	file := filepath.Join(cwd, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	settings := map[string]any{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s isn't valid JSON: %w", file, err)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	entry := func(matcher string) map[string]any {
		value := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 3}}}
		if matcher != "" {
			value["matcher"] = matcher
		}
		return value
	}
	for _, wanted := range []struct{ event, matcher string }{{"PreToolUse", ".*"}, {"PostToolUse", "Agent|Task"}, {"SubagentStart", ""}, {"SubagentStop", ""}} {
		list, _ := hooks[wanted.event].([]any)
		present := false
		for _, item := range list {
			group, _ := item.(map[string]any)
			inner, _ := group["hooks"].([]any)
			for _, hook := range inner {
				if h, _ := hook.(map[string]any); h != nil && h["command"] == command {
					present = true
				}
			}
		}
		if !present {
			list = append(list, entry(wanted.matcher))
		}
		hooks[wanted.event] = list
	}
	settings["hooks"] = hooks
	raw, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(file, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	say(c, "Hooks written to %s. Claude Code started here now reports its tools and sub-agents to Forge (FORGE_RUNNER must be set).", file)
	return nil
}

// deliveryHook is the Claude Code hook: it tells Forge which tool the agent is running and what its
// sub-agents do. It never blocks or fails the harness: any error is swallowed.
func deliveryHook(c *deliveryClient, _ deliveryArgs, cmd *cobra.Command) error {
	runner := os.Getenv("FORGE_RUNNER")
	if runner == "" {
		return nil
	}
	var input map[string]any
	if err := json.NewDecoder(cmd.InOrStdin()).Decode(&input); err != nil {
		return nil
	}
	report := hookReport(input)
	if report == nil {
		return nil
	}
	report["runner"] = runner
	body, _ := json.Marshal(report)
	client := &http.Client{Timeout: 1500 * time.Millisecond, Transport: baseTransport}
	if response, err := client.Post(c.url("/api/harness", nil), "application/json", bytes.NewReader(body)); err == nil {
		response.Body.Close()
	}
	return nil
}

func hookSummary(toolInput any) any {
	fields, ok := toolInput.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"description", "command", "file_path", "pattern", "url", "query"} {
		if value, ok := fields[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func hookReport(input map[string]any) map[string]any {
	switch input["hook_event_name"] {
	case "SubagentStart":
		return map[string]any{"event": "start", "id": input["agent_id"], "type": input["agent_type"]}
	case "SubagentStop":
		return map[string]any{"event": "stop", "id": input["agent_id"], "result": input["last_assistant_message"], "transcript": input["agent_transcript_path"]}
	case "PostToolUse":
		if input["tool_name"] != "Agent" && input["tool_name"] != "Task" {
			return nil
		}
		response, _ := input["tool_response"].(map[string]any)
		id := response["agentId"]
		if id == nil {
			id = response["agent_id"]
		}
		if id == nil {
			return nil
		}
		toolInput, _ := input["tool_input"].(map[string]any)
		return map[string]any{"event": "describe", "id": id, "description": toolInput["description"]}
	case "PreToolUse":
		if id, ok := input["agent_id"]; ok && id != nil {
			return map[string]any{"event": "tool", "id": id, "tool": input["tool_name"], "summary": hookSummary(input["tool_input"])}
		}
		return map[string]any{"event": "self", "tool": input["tool_name"], "summary": hookSummary(input["tool_input"])}
	}
	return nil
}
