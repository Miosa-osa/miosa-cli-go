package commands

// Forge Delivery from the terminal: Missions, phases, Work Orders, the crew of agents that work
// them, and the person steering. Every command is one call to Forge Delivery in the MIOSA API, with
// the signed-in key and organization. FORGE_LOCAL_URL points the commands at a local forge-local
// service instead (development); it gets no MIOSA credential.
//
// The commands keep their own argument parsing (DisableFlagParsing): flags may come before or after
// the words, `--option` repeats, and `miosa forge check run MIO-12 -- <command…>` passes everything
// after `--` through untouched. Agents and the crew prompts rely on exactly this shape.

import (
	"bytes"
	"context"
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

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

const forgeDeliveryGroup = "delivery"

// Flags that take no value; every other --flag takes the word after it.
var deliveryBoolFlags = map[string]bool{"mine": true, "ready": true, "standing": true, "full": true, "new": true, "organization": true, "backlog": true, "all": true, "wait": true, "important": true}

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
	// An Issue key is MIO-12 (the glossary's name); MIO-12, the old spelling, still reads.
	trimmed := regexp.MustCompile(`(?i)^(?:mio|wo)-?`).ReplaceAllString(value, "")
	if n, err := strconv.Atoi(trimmed); err == nil {
		return n
	}
	return nil
}

func woLabel(id int) string { return fmt.Sprintf("MIO-%d", id) }

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

// runner is the running agent this terminal is: --runner, then FORGE_RUNNER, then the id
// `miosa forge join` saved for this worktree (harness shells do not keep an export).
func (a deliveryArgs) runner() string {
	return firstNonEmpty(a.flag("runner"), os.Getenv("FORGE_RUNNER"), savedRunner())
}

// ---- the Delivery service ----

// deliveryAPIPath is Forge Delivery in the MIOSA API (relative to its base URL, like every other
// command's path). The actions keep forge-local's names and payloads.
const deliveryAPIPath = "/forge/delivery"

// errDeliveryUnavailable is what an account without Forge Delivery sees, from every command.
var errDeliveryUnavailable = errors.New("the Forge Delivery API isn't available for this account yet")

type deliveryClient struct {
	// local is set when FORGE_LOCAL_URL names a forge-local service: requests go there, with no
	// MIOSA credential and no platform org header. Otherwise they go to the MIOSA API with the
	// signed-in key and organization.
	local string
	org   string
	out   io.Writer
	http  *http.Client
	api   *api.Client
	// wait bounds each request; zero means 30 s. The Claude Code hooks set a few seconds.
	wait time.Duration
}

func (c *deliveryClient) timeout() time.Duration {
	if c.wait > 0 {
		return c.wait
	}
	return 30 * time.Second
}

func newDeliveryClient(a deliveryArgs, out io.Writer) *deliveryClient {
	// Delivery commands parse their own words (DisableFlagParsing), so the global flags they
	// accept are applied here.
	if value := a.flag("api-key"); value != "" {
		globalFlags.APIKey = value
	}
	if value := a.flag("api-url"); value != "" {
		globalFlags.APIURL = value
	}
	if value := a.flag("org"); value != "" {
		orgFlag = value
	}
	local := strings.TrimRight(os.Getenv("FORGE_LOCAL_URL"), "/")
	org := ""
	if local != "" {
		// FORGE_ORG names forge-local's Delivery directly; otherwise the organization this CLI works in.
		org = firstNonEmpty(a.flag("org"), os.Getenv("FORGE_ORG"), effectiveOrgFlag())
	}
	return &deliveryClient{
		local: local,
		org:   org,
		out:   out,
		// forge-local is not the platform API: no MIOSA org headers, and no "org: …, workspace: …" line.
		http: &http.Client{Timeout: 30 * time.Second, Transport: baseTransport},
	}
}

// platform is the MIOSA API client, built on first use with the user's key; the organization
// travels as X-Miosa-Org and the Delivery API refuses one the key does not act in. No extra
// round trip to check it first: the Claude Code hook calls this on every tool use.
func (c *deliveryClient) platform() (*api.Client, error) {
	if c.api == nil {
		built, _, err := buildPublicClient()
		if err != nil {
			return nil, err
		}
		if err := built.MustAuthenticated(); err != nil {
			return nil, err
		}
		c.api = built.API
	}
	return c.api, nil
}

func (c *deliveryClient) localURL(path string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	if c.org != "" {
		query.Set("org", c.org)
	}
	if encoded := query.Encode(); encoded != "" {
		return c.local + "/api" + path + "?" + encoded
	}
	return c.local + "/api" + path
}

func (c *deliveryClient) unreachable() error {
	return fmt.Errorf("forge-local isn't running at %s. Start it: node tools/forge-local/server.mjs (in MiosaOS), or unset FORGE_LOCAL_URL to use MIOSA", c.local)
}

// send makes one request to Delivery and returns the raw JSON body. path is relative to
// Delivery ("/state", "/actions/add"; forge-local: "/add"); payload nil means GET.
func (c *deliveryClient) send(path string, query url.Values, payload map[string]any, timeout time.Duration) ([]byte, error) {
	for key, value := range payload {
		if value == omitted {
			delete(payload, key)
		}
	}
	if c.local == "" {
		client, err := c.platform()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		request := api.Request{Method: http.MethodGet, Path: deliveryAPIPath + path, Query: query, Verbatim: true}
		if payload != nil {
			request.Method, request.Body = http.MethodPost, payload
		}
		body, _, err := client.Bytes(ctx, request)
		// Forge off for the organization, or an API without Delivery: no route at all.
		if api.IsCode(err, "FORGE_DISABLED") || (api.IsCode(err, "NOT_FOUND") && !strings.HasPrefix(path, "/profiles/")) {
			return nil, errDeliveryUnavailable
		}
		return body, err
	}
	var response *http.Response
	var err error
	client := &http.Client{Timeout: timeout, Transport: c.http.Transport}
	if payload == nil {
		response, err = client.Get(c.localURL(path, query))
	} else {
		body, _ := json.Marshal(payload)
		response, err = client.Post(c.localURL(path, nil), "application/json", bytes.NewReader(body))
	}
	if err != nil {
		return nil, c.unreachable()
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, c.unreachable()
	}
	if response.StatusCode >= 400 && payload == nil {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		return nil, errors.New(firstNonEmpty(failure.Error, response.Status))
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("the Forge Delivery API answered %s with something that isn't JSON", response.Status)
	}
	return body, nil
}

// call runs one Delivery action and decodes its result into out (nil to ignore it).
func (c *deliveryClient) call(action string, payload map[string]any, out any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	path := "/" + action
	if c.local == "" {
		path = "/actions/" + action
	}
	body, err := c.send(path, nil, payload, c.timeout())
	if err != nil {
		return err
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("the Forge Delivery API answered with something that isn't JSON")
	}
	if !envelope.OK {
		return errors.New(firstNonEmpty(envelope.Error, "Forge Delivery refused that"))
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	if c.local == "" {
		envelope.Result = deliveryLegacyJSON(envelope.Result, false)
	}
	return json.Unmarshal(envelope.Result, out)
}

// get reads a Delivery path, named as forge-local names it ("/state", "/docs/agents",
// "/profile/<id>?v=<n>"); the API's answers come in {"data": …} and are renamed.
func (c *deliveryClient) get(path string, query url.Values, out any) error {
	board := path == "/state"
	if c.local == "" {
		if id, ok := strings.CutPrefix(path, "/profile/"); ok {
			path = "/profiles/" + id
			if v := query.Get("v"); v != "" {
				query = url.Values{"version": {v}}
			}
		}
	}
	body, err := c.send(path, query, nil, c.timeout())
	if err != nil {
		return err
	}
	if c.local == "" {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 {
			return fmt.Errorf("the Forge Delivery API answered with something that isn't JSON")
		}
		body = deliveryLegacyJSON(envelope.Data, board)
	}
	return json.Unmarshal(body, out)
}

func (c *deliveryClient) state() (*deliveryState, error) {
	var s deliveryState
	if err := c.get("/state", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ---- the board, as much of it as the commands read ----

type deliveryText struct {
	Text string `json:"text"`
}

type deliveryState struct {
	Missions    []deliveryMission    `json:"missions"`
	Initiatives []deliveryInitiative `json:"initiatives"`
	Domains     []deliveryDomain     `json:"domains"`
	Milestones  []deliveryMilestone  `json:"milestones"`
	Orders      []deliveryOrder      `json:"orders"`
	Crew        []deliveryRole       `json:"crew"`
	Runners     []deliveryRunner     `json:"runners"`
	Decisions   []deliveryDecision   `json:"decisions"`
	Releases    []deliveryRelease    `json:"releases"`
	Intake      []deliveryNote       `json:"intake"`
	Customers   []deliveryCustomer   `json:"customers"`
	Charter     *deliveryText        `json:"charter"`
	Activity    []deliveryActivity   `json:"activity"`
}

type deliveryInitiative struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Goal     string   `json:"goal"`
	Owner    string   `json:"owner"`
	Status   string   `json:"status"`
	Health   string   `json:"health"`
	Missions []string `json:"missions"`
	Counts   struct {
		Planned        int `json:"planned"`
		PlannedShipped int `json:"plannedShipped"`
		Added          int `json:"added"`
		Discovered     int `json:"discovered"`
	} `json:"counts"`
}

type deliveryMission struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Status   string        `json:"status"`
	Goal     string        `json:"goal"`
	DoneWhen []string      `json:"doneWhen"`
	Brief    *deliveryText `json:"brief"`
	// Owner is the person who answers for it; Health is computed by the service from the line.
	Owner   string           `json:"owner"`
	Health  *deliveryHealth  `json:"health"`
	Updates []deliveryUpdate `json:"updates"`
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
	Type      string `json:"type"`
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
	// HumanOwner is the person who owns it; Owner is the Crew role that does it (the delegate).
	HumanOwner string `json:"humanOwner"`
	// Relations (section 4, RELATIONS) and customers' requests (CUSTOMERS).
	DuplicateOf *int              `json:"duplicateOf"`
	Related     []int             `json:"related"`
	Parent      *int              `json:"parent"`
	Requests    []deliveryRequest `json:"requests"`
	// Domain it belongs to, the Component (repository) its branch goes to, and its triage clock.
	Domain    string          `json:"domain"`
	Component string          `json:"component"`
	Triage    *deliveryTriage `json:"triage"`
}

// owners is "Primary owner: <person> · delegate <role>", the person first (E1).
func (o *deliveryOrder) owners() string {
	return fmt.Sprintf("Primary owner: %s · delegate %s", firstNonEmpty(o.HumanOwner, "none yet (set when a person approves the spec)"), o.Owner)
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
	_, _ = fmt.Fprintf(c.out, format+"\n", values...)
}

func deliveryCommands() []deliveryCommand {
	commands := []deliveryCommand{
		{"prime", "What any agent harness reads first: the rules and where things stand", deliveryPrime},
		{"status", "The active Mission, its phases, and who is on what", deliveryStatus},
		{"ls", "Issues by status", deliveryList},
		{"inbox", "What needs you: decisions, questions, approvals, promotions", deliveryInbox},
		{"show MIO-12", "One Issue: spec, steps, activity", deliveryShow},
		{"context MIO-12", "What to read before working: Mission, phase, Issue [--full]", deliveryContext},
		{"charter", "The Charter above every Mission: how MIOSA ships, what comes first", deliveryCharter},
		{"brief", "The Mission's brief, or a phase's", deliveryBrief},
		{"intake", "Notes left in Forge waiting to be planned", deliveryIntake},
		{"planned <note> MIO-12…", "Link a note to the Issues it became (or --reason \"why none\")", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("planned", map[string]any{"id": a.arg(0), "workOrders": a.rest(1), "reason": a.flag("reason"), "by": a.actor()}, nil)
		}},
		{"add \"text\"", "Add work: split into Issues, deduplicated, queued", deliveryAdd},
		{"spec MIO-12", "Write an Issue's spec, a draft for the person to approve", deliverySpecCmd},
		{"mission new|start|close|edit|import", "Create, start, close, edit or import a Mission", deliveryMissionCmd},
		{"edit MIO-12", "Edit an Issue", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			issueType, err := deliveryIssueType(a.flag("type"))
			if err != nil {
				return err
			}
			return c.call("editOrder", map[string]any{"id": woNumber(a.arg(0)), "title": optional(a.flag("title")), "detail": optional(a.flag("detail")), "doneWhen": optional(a.flag("done-when")), "milestone": optional(a.flag("milestone")), "owner": optional(a.flag("owner")), "type": issueType, "by": a.actor()}, nil)
		}},
		{"milestone add|edit|branch|merged", "Phases: add one, edit it, give it a branch, record that branch's merge", deliveryMilestoneCmd},
		{"initiative new|add|show", "Initiatives: Missions under one strategy; new, add a Mission, show the roll-up", deliveryInitiativeCmd},
		{"domain new|set|show", "Domains: new, set owner and triage person, show Components and Backlog", deliveryDomainCmd},
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
		{"verify <milestone> \"evidence\"", "Sign off a phase: its acceptance criteria, checked against real evidence", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if err := c.call("verify", map[string]any{"milestone": a.arg(0), "evidence": strings.Join(a.rest(1), " "), "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "Checkpoint %s verified.", a.arg(0))
			return nil
		}},
		{"join", "Join a Mission's crew as a running agent", deliveryJoin},
		{"take [MIO-12]", "Hold an Issue (the next in your queue if omitted)", deliveryTake},
		{"assist MIO-12", "Work one step of an Issue another agent holds", deliveryAssist},
		{"leave", "Stop this running agent; what it held goes back to the queue", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			runner := a.runner()
			if runner == "" {
				return errors.New("which running agent? --runner <id> or FORGE_RUNNER")
			}
			if err := c.call("leave", map[string]any{"runner": runner}, nil); err != nil {
				return err
			}
			if savedRunner() == runner {
				clearRunner()
			}
			say(c, "%s left. Anything it held is back in the queue.", runner)
			return nil
		}},
		{"steps MIO-12 \"a\" \"b\"…", "Plan its steps", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if err := c.call("steps", map[string]any{"id": woNumber(a.arg(0)), "steps": a.rest(1)}, nil); err != nil {
				return err
			}
			say(c, "Planned %d steps.", len(a.rest(1)))
			return nil
		}},
		{"step MIO-12 \"a\" | 2 | 2 start", "Mark a step done, or in progress, by title or number", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("step", map[string]any{"id": woNumber(a.arg(0)), "title": strings.Join(a.rest(1), " "), "by": optional(firstNonEmpty(a.flag("by"), a.runner()))}, nil)
		}},
		{"log MIO-12 \"text\"", "A line in its activity", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call("log", map[string]any{"id": woNumber(a.arg(0)), "text": strings.Join(a.rest(1), " "), "actor": optional(firstNonEmpty(a.flag("as"), a.runner(), os.Getenv("FORGE_ACTOR")))}, nil)
		}},
		{"ask MIO-12 \"question\"", "Ask the person, going ahead with your own call unless they steer", deliveryAsk},
		{"wait MIO-12", "Block until the person answers (or your own call applies), then print it", deliveryWait},
		{"push MIO-12", "Record the branch and commit you just pushed (read from git)", deliveryPush},
		{"done MIO-12", "Hand over with a completion report", deliveryDone},
		{"merged MIO-12 <sha|PR>", "Record a real merge", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if a.arg(1) == "" {
				return errors.New("give the merge commit or PR link: miosa forge merged MIO-12 <sha|PR link>")
			}
			if err := c.call("merged", map[string]any{"id": woNumber(a.arg(0)), "ref": a.arg(1), "into": optional(a.flag("into")), "by": firstNonEmpty(a.flag("as"), a.runner(), os.Getenv("FORGE_ACTOR"), "you")}, nil); err != nil {
				return err
			}
			say(c, "Recorded the merge of %s.", woLabel(woNumber(a.arg(0)).(int)))
			return nil
		}},
		{"discover \"what\"", "File something you found outside your Issue", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
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
		{"place MIO-51", "Place a discovery in a Mission and phase", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			var result struct {
				Where string `json:"where"`
			}
			if err := c.call("placeOrder", map[string]any{"id": woNumber(a.arg(0)), "mission": optional(a.flag("mission")), "milestone": optional(a.flag("milestone")), "owner": optional(a.flag("owner")), "priority": optional(a.flag("priority")), "domain": optional(a.flag("domain")), "why": optional(a.flag("why")), "by": firstNonEmpty(a.flag("as"), a.runner(), a.actor())}, &result); err != nil {
				return err
			}
			label := woLabel(woNumber(a.arg(0)).(int))
			say(c, "Placed %s: %s. Draft its spec next: miosa forge spec %s --json <file>", label, result.Where, label)
			return nil
		}},
		{"assign <agent>", "Move an agent to another Mission's crew", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if err := c.call("assign", map[string]any{"runner": a.arg(0), "mission": optional(a.flag("mission")), "why": a.flag("why"), "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "%s is in the %s crew now.", a.arg(0), a.flag("mission"))
			return nil
		}},
		{"seats <mission> <role> <n>", "Agent capacity: how many agents of a role a Mission's crew has", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			count, err := strconv.Atoi(a.arg(2))
			if a.arg(0) == "" || a.arg(1) == "" || err != nil {
				return errors.New("usage: miosa forge seats <mission> <role> <count>")
			}
			if err := c.call("setSeats", map[string]any{"mission": a.arg(0), "role": a.arg(1), "count": count, "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "%s: agent capacity for %s is %d.", a.arg(0), count, a.arg(1))
			return nil
		}},
		{"prompt [profile]", "The prompt this agent is pinned to (its Mission's, at its version)", deliveryPrompt},
		{"repin [agent]", "Switch to the latest version of its Mission's prompt", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			id := firstNonEmpty(a.arg(0), a.runner())
			if id == "" {
				return errors.New("usage: miosa forge repin <agent>")
			}
			if err := c.call("repin", map[string]any{"runner": id, "by": a.actor()}, nil); err != nil {
				return err
			}
			say(c, "%s now uses the latest prompt. Re-read it: miosa forge prompt", id)
			return nil
		}},
		{"connect", "Connect this repository's Claude Code to Forge Delivery: hooks, sign-in, name", deliveryConnect},
		{"hooks install", "In an agent's folder: Claude Code keeps to its Forge Issue and reports live", deliveryHooksInstall},
		{"hook", "Claude Code hook (called by Claude Code, not by people)", deliveryHook},
		{"watch", "The live feed of everything that happens", deliveryWatch},
		{"approve-spec MIO-12 | --milestone id", "Approve draft specs (agents can't start without one)", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			// The service refuses an agent's approval, so it is told who approves.
			payload := map[string]any{"id": woNumber(a.arg(0)), "by": a.actor(), "owner": optional(a.flag("owner"))}
			if milestone := a.flag("milestone"); milestone != "" {
				payload = map[string]any{"milestone": milestone, "by": a.actor(), "owner": optional(a.flag("owner"))}
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
	commands = append(commands, deliveryRelationCommands()...)
	// You, steering: one line each.
	for _, action := range []string{"answer", "approve", "next", "end", "backlog", "cancel", "accept", "dismiss", "retry", "note"} {
		action := action
		commands = append(commands, deliveryCommand{action + " MIO-12 [text]", "Steer an Issue: " + action, func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			return c.call(action, map[string]any{"id": woNumber(a.arg(0)), "text": strings.Join(a.rest(1), " ")}, nil)
		}})
	}
	for _, priority := range []string{"p0", "p1", "p2", "p3"} {
		priority := priority
		commands = append(commands, deliveryCommand{priority + " MIO-12", "Set the priority to " + strings.ToUpper(priority), func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
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
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓")
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
// a Work Order's Check is `miosa forge check run MIO-12 -- <command…>`.
func addForgeDeliveryCmds(forge *cobra.Command) {
	forge.AddGroup(&cobra.Group{ID: forgeDeliveryGroup, Title: "Delivery (Missions, Issues, crew):"})
	for _, command := range deliveryCommands() {
		forge.AddCommand(deliveryCobra(command))
	}
	for _, sub := range forge.Commands() {
		if sub.Name() == "check" {
			run := deliveryCobra(deliveryCommand{"run MIO-12 -- <command…>", "Run a real Check on an Issue and record the result", deliveryCheckRun})
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
	if err := c.get("/docs/agents", nil, &guide); err != nil {
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
		say(c, "- %s  %s  %d/%d done%s\n    Acceptance criteria: %s", milestone.ID, milestone.Title, done, len(items), verified, milestone.DoneWhen)
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
	health := ""
	if mission := s.activeMission(); mission != nil {
		title = mission.Title
		health = "  " + healthWord(p, mission.Health)
		if mission.Owner != "" {
			health += p.dim + " · owner " + mission.Owner + p.reset
		}
	}
	say(c, "%s%s%s  %d/%d shipped%s", p.bold, title, p.reset, shipped, planned, health)
	if mission := s.activeMission(); mission != nil && mission.Health != nil {
		for _, reason := range mission.Health.Reasons {
			say(c, "  %s! %s%s", p.yellow, reason, p.reset)
		}
	}
	say(c, "%s%s%s  %d/%d deployed", p.bold, title, p.reset, shipped, planned)
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
	issueType := strings.ToLower(a.flag("type"))
	for _, status := range deliveryStateOrder {
		if status == "shipped" && !a.has("all") {
			continue
		}
		var items []deliveryOrder
		for _, order := range s.Orders {
			if !order.Dismissed && order.InMission != a.has("backlog") && (owner == "" || order.Owner == owner) && (issueType == "" || order.Type == issueType) && order.State == status {
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
	// T-RESP: new Issues on the triage clock, with their deadline; a breach says how late.
	now := time.Now()
	for _, order := range s.Orders {
		if line := triageLine(order, now); line != "" && !order.Dismissed {
			count++
			say(c, "%striage%s %s %s (%s) %s   → miosa forge place %s …", p.yellow, p.reset, woLabel(order.ID), order.Title, firstNonEmpty(order.Domain, "no Domain"), line, woLabel(order.ID))
		}
	}
	// M-HEALTH: a Mission that flipped to off track needs a person.
	for _, mission := range s.Missions {
		if mission.Health != nil && mission.Health.State == "off_track" {
			count++
			say(c, "%soff track%s %s: %s", p.red, p.reset, mission.Title, strings.Join(mission.Health.Reasons, "; "))
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
		return fmt.Errorf("no Issue %s", a.arg(0))
	}
	p := deliveryColors(c.out)
	branch := "no branch"
	if order.Branch != nil {
		branch = *order.Branch
	}
	say(c, "%s%s %s%s", p.bold, woLabel(order.ID), order.Title, p.reset)
	say(c, "%s · %s · %s · %s · %s · Checks %s", deliveryStateLabel[order.State], firstNonEmpty(order.Type, "task"), order.Priority, order.owners(), branch, order.Checks)
	if order.Detail != "" {
		say(c, "\n%s", order.Detail)
	}
	say(c, "\nAcceptance criteria: %s", order.DoneWhen)
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
	if lines := relationLines(s, order); len(lines) > 0 {
		say(c, "\n%sRelations%s\n%s", p.bold, p.reset, strings.Join(lines, "\n"))
	}
	if lines := requestLines(s, order); len(lines) > 0 {
		say(c, "\n%sCustomers who asked%s\n%s", p.bold, p.reset, strings.Join(lines, "\n"))
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
		return fmt.Errorf("no Issue %s", a.arg(0))
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
		say(c, "# 1. Mission: %s\n\nGoal: %s\nAcceptance criteria:\n%s", mission.Title, firstNonEmpty(mission.Goal, "(none)"), doneWhen)
		if mission.Brief != nil && mission.Brief.Text != "" {
			if a.has("full") {
				say(c, "\n%s", mission.Brief.Text)
			} else {
				say(c, "\nThe full brief (%d lines) is the source of truth for this Mission: miosa forge brief", len(strings.Split(mission.Brief.Text, "\n")))
			}
		}
	} else {
		say(c, "# 1. Mission: none\n\nGoal: (none)\nAcceptance criteria:\n- (not set)")
	}
	if milestone := s.milestone(order.Milestone); milestone != nil {
		brief := ""
		if milestone.Brief != nil && milestone.Brief.Text != "" {
			brief = "\n\n" + milestone.Brief.Text
		}
		say(c, "\n# 2. Phase: %s\n\nAcceptance criteria: %s%s", milestone.Title, milestone.DoneWhen, brief)
	}
	specState := "none"
	if order.SpecState != nil {
		specState = *order.SpecState
	}
	say(c, "\n# 3. %s: %s\n\n%s · %s · spec %s\nAcceptance criteria: %s", woLabel(order.ID), order.Title, order.owners(), order.Priority, specState, order.DoneWhen)
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
	say(c, `Plan each into Work Orders (miosa forge add … then miosa forge spec …), then: miosa forge planned <note> MIO-<n> [MIO-<m>…]  or  miosa forge planned <note> --reason "why none"`)
	return nil
}

// deliveryIssueTypes are an Issue's types: bug, feature or task (Delivery's default).
var deliveryIssueTypes = map[string]bool{"bug": true, "feature": true, "task": true}

// deliveryIssueType checks --type; empty leaves it out of the payload.
func deliveryIssueType(value string) (any, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return omitted, nil
	}
	if !deliveryIssueTypes[value] {
		return nil, fmt.Errorf("--type is bug, feature or task, not %q", value)
	}
	return value, nil
}

func deliveryAdd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	var results []struct {
		ID      int    `json:"id"`
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	}
	issueType, err := deliveryIssueType(a.flag("type"))
	if err != nil {
		return err
	}
	payload := map[string]any{"text": strings.Join(a.positional, " "), "owner": optional(a.flag("owner")), "priority": optional(a.flag("priority")), "milestone": optional(a.flag("milestone")), "domain": optional(a.flag("domain")), "mission": optional(a.flag("mission")), "type": issueType, "isNew": a.has("new"), "by": a.actor()}
	// A customer's report: the request rides on the Issue it creates, or on the one it duplicates.
	if customer := a.flag("customer"); customer != "" {
		payload["customer"] = customer
		payload["important"] = a.has("important")
		payload["source"] = a.flag("source")
	}
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
		return errors.New("give the spec as JSON: miosa forge spec MIO-12 --json spec.json (or - for stdin)")
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

func deliveryMissionCmd(c *deliveryClient, a deliveryArgs, cmd *cobra.Command) error {
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
		var result struct {
			ID string `json:"id"`
		}
		if err := c.call("createMission", map[string]any{"title": strings.Join(rest, " "), "goal": a.flag("goal"), "doneWhen": splitList(a.flag("done-when"), "|"), "kind": kind, "owner": optional(a.flag("owner")), "owners": owners, "scope": missionScope(a), "by": a.actor()}, &result); err != nil {
			return err
		}
		if standing {
			say(c, "Domain %s: always open. Add work with miosa forge add \"…\" --mission %s", result.ID, result.ID)
		} else {
			say(c, "Mission %s (planned). Add phases and Issues, then: miosa forge mission start %s", result.ID, result.ID)
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
	case "import":
		return deliveryMissionImport(c, a, first)
	case "owner":
		// The person who answers for the Mission (Linear's project lead); the service refuses an agent.
		if first == "" || len(rest) < 2 {
			return errors.New("miosa forge mission owner <mission> <person>")
		}
		if err := c.call("missionOwner", map[string]any{"id": first, "owner": strings.Join(rest[1:], " "), "by": a.actor()}, nil); err != nil {
			return err
		}
		say(c, "%s owns %s.", strings.Join(rest[1:], " "), first)
		return nil
	case "update":
		// The weekly update the Orchestrator writes from the Records; the health of the moment is recorded with it.
		if first == "" {
			return errors.New(`miosa forge mission update <mission> "what moved, what did not, why" | --text <file|->`)
		}
		text := strings.Join(rest[1:], " ")
		if source := a.flag("text"); source != "" {
			raw, err := readSource(source, cmd.InOrStdin())
			if err != nil {
				return err
			}
			text = strings.TrimSpace(string(raw))
		}
		var update deliveryUpdate
		if err := c.call("missionUpdate", map[string]any{"id": first, "text": text, "by": a.actor()}, &update); err != nil {
			return err
		}
		say(c, "Update written for %s (%s).", first, firstNonEmpty(deliveryHealthLabel[update.Health], "health not measured"))
		return nil
	}
	return errors.New("miosa forge mission new|start|close|edit|owner|update|import …")
}

// missionScope is where a new Mission lives: --organization, --workspace <id>, or nil (Delivery's default).
func missionScope(a deliveryArgs) any {
	if a.has("organization") {
		return map[string]any{"kind": "organization"}
	}
	workspace := a.flag("workspace")
	if workspace == "" {
		return nil
	}
	scope := map[string]any{"kind": "workspace", "id": workspace}
	if name := a.flag("workspace-name"); name != "" {
		scope["name"] = name
	}
	return scope
}

var planTitle = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// deliveryMissionImport loads a plan file's checklist ("### A. Title" sections, "| A1 | … | **State** | … |"
// rows) as a Mission: one phase per section, one Work Order per row. Re-running it updates the same
// Work Orders from the file, keyed on the row id.
func deliveryMissionImport(c *deliveryClient, a deliveryArgs, file string) error {
	if file == "" {
		return errors.New("miosa forge mission import <plan.md> [--title …] [--goal …] [--done-when \"a|b\"] [--owner-map owners.json]")
	}
	text, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	title := a.flag("title")
	if title == "" {
		if match := planTitle.FindStringSubmatch(string(text)); match != nil {
			title = match[1]
		}
	}
	var owners any = omitted
	if path := a.flag("owner-map"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &owners); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	var doneWhen any = omitted
	if value := a.flag("done-when"); value != "" {
		doneWhen = strings.Split(value, "|")
	}
	var result struct {
		Mission        string `json:"mission"`
		MissionCreated bool   `json:"missionCreated"`
		Milestones     []struct {
			Title string `json:"title"`
			Done  int    `json:"done"`
			Total int    `json:"total"`
		} `json:"milestones"`
		Created int      `json:"created"`
		Updated int      `json:"updated"`
		Moved   int      `json:"moved"`
		Held    []string `json:"held"`
	}
	payload := map[string]any{"text": string(text), "source": filepath.Base(file), "title": title, "goal": optional(a.flag("goal")), "doneWhen": doneWhen, "owners": owners, "by": a.actor()}
	if scope := missionScope(a); scope != nil {
		payload["scope"] = scope
	}
	if err := c.call("importPlan", payload, &result); err != nil {
		return err
	}
	for _, milestone := range result.Milestones {
		say(c, "%s: %d/%d Done", milestone.Title, milestone.Done, milestone.Total)
	}
	verb := "Updated"
	if result.MissionCreated {
		verb = "Created"
	}
	held := ""
	if len(result.Held) > 0 {
		held = fmt.Sprintf(", %s held by agents (left as they report)", strings.Join(result.Held, ", "))
	}
	say(c, "%s %s: %d new Issues, %d updated, %d changed state%s.", verb, result.Mission, result.Created, result.Updated, result.Moved, held)
	return nil
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
		say(c, "Phase %s.", result.ID)
		return nil
	}
	return errors.New(`miosa forge milestone add "title" --done-when "…" [--after id] | miosa forge milestone edit <id> [--title] [--done-when]`)
}

// An Initiative groups several Missions under one strategy (glossary, section 2). Its progress
// is the roll-up of its Missions': planned against the baseline, added and discovered kept apart.
func deliveryInitiativeCmd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	switch a.arg(0) {
	case "new":
		missions := []string{}
		for _, id := range strings.Split(a.flag("missions"), ",") {
			if id = strings.TrimSpace(id); id != "" {
				missions = append(missions, id)
			}
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := c.call("createInitiative", map[string]any{"title": strings.Join(a.rest(1), " "), "goal": a.flag("goal"), "missions": missions, "owner": optional(a.flag("owner")), "by": a.actor()}, &result); err != nil {
			return err
		}
		say(c, "Initiative %s.", result.ID)
		return nil
	case "add":
		if a.arg(1) == "" || a.arg(2) == "" {
			return errors.New("miosa forge initiative add <initiative> <mission>")
		}
		return c.call("initiativeAdd", map[string]any{"id": a.arg(1), "mission": a.arg(2), "by": a.actor()}, nil)
	case "show":
		s, err := c.state()
		if err != nil {
			return err
		}
		shown := 0
		for _, initiative := range s.Initiatives {
			if a.arg(1) != "" && initiative.ID != a.arg(1) {
				continue
			}
			shown++
			percent := 0
			if initiative.Counts.Planned > 0 {
				percent = initiative.Counts.PlannedShipped * 100 / initiative.Counts.Planned
			}
			say(c, "%s  %s", initiative.ID, initiative.Title)
			if initiative.Goal != "" {
				say(c, "  %s", initiative.Goal)
			}
			// Status is read from its Missions; health is their health rolled up (grey, green, yellow, red).
			say(c, "  %s · health %s · owner %s", firstNonEmpty(initiative.Status, "proposed"), firstNonEmpty(initiative.Health, "grey"), firstNonEmpty(initiative.Owner, "not set"))
			say(c, "  %d%% · %d of %d planned shipped · added %d · discovered %d", percent, initiative.Counts.PlannedShipped, initiative.Counts.Planned, initiative.Counts.Added, initiative.Counts.Discovered)
			for _, id := range initiative.Missions {
				for _, mission := range s.Missions {
					if mission.ID == id {
						health := ""
						if mission.Health != nil {
							health = " · " + deliveryHealthLabel[mission.Health.State]
						}
						say(c, "  - %s (%s%s) %s", mission.ID, mission.Status, health, mission.Title)
					}
				}
			}
		}
		if shown == 0 {
			if a.arg(1) != "" {
				return fmt.Errorf("no Initiative %s", a.arg(1))
			}
			say(c, "No Initiatives yet: miosa forge initiative new \"One delivery system\" --goal \"…\" --missions a,b")
		}
		return nil
	}
	return errors.New(`miosa forge initiative new "title" [--goal "…"] [--missions id,id] | miosa forge initiative add <initiative> <mission> | miosa forge initiative show [<initiative>]`)
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
	saved := ""
	if saveRunner(result.Runner) {
		saved = " (saved for this worktree)"
	}
	say(c, "You are %s%s, in the %s crew. %d Issues for your role in this Mission.\nRead your prompt: miosa forge prompt   Then: miosa forge take", result.Runner, saved, result.Mission, len(result.Queue))
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
		return errors.New("miosa forge assist MIO-12 --step 3   (work one step of an Issue someone else holds)")
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
		return errors.New("give the command after --: miosa forge check run MIO-12 -- pnpm test")
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
		return fmt.Errorf("hand over with a completion report (JSON): miosa forge done %s --report report.json (or - for stdin)%s", firstNonEmpty(a.arg(0), "MIO-12"), deliveryReportShape)
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
	say(c, "\nPlace each: miosa forge place MIO-<n> --milestone <phase> --owner <role> --priority P0-P3 --why \"…\"   (miosa forge context MIO-<n> first)")
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
	if err := c.get("/profile/"+url.PathEscape(profile), query, &result); err != nil {
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
