package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Domains (glossary, section 1): the forever, owned parts of the platform. A person creates one
// and sets who answers for it and who is on triage duty; agents set it on Issues. Its Components
// say which repository and trunk an Issue's branch goes to.

type deliveryComponent struct {
	Repo          string `json:"repo"`
	Trunk         string `json:"trunk"`
	ReleaseScheme string `json:"releaseScheme"`
}

type deliveryDomain struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Owner      string              `json:"owner"`
	Crew       deliveryCrew        `json:"crew"`
	Triage     string              `json:"triage"`
	SLA        map[string]int      `json:"sla"`
	Components []deliveryComponent `json:"components"`
	Standing   string              `json:"standing"`
}

// deliveryCrew is a Domain's crew: the role that does its work by default and how many agents
// of each role it has. The Delivery API writes it as {"role": …, "capacity": {…}} (renamed to
// "seats" on the way in); older forge-local boards write just the role as a string.
type deliveryCrew struct {
	Role     string
	Capacity map[string]int
}

func (crew *deliveryCrew) UnmarshalJSON(raw []byte) error {
	*crew = deliveryCrew{}
	if string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		return json.Unmarshal(raw, &crew.Role)
	}
	var object struct {
		Role     string         `json:"role"`
		Capacity map[string]int `json:"capacity"`
		Seats    map[string]int `json:"seats"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	crew.Role, crew.Capacity = object.Role, object.Capacity
	if crew.Capacity == nil {
		crew.Capacity = object.Seats
	}
	return nil
}

func (crew deliveryCrew) MarshalJSON() ([]byte, error) {
	capacity := crew.Capacity
	if capacity == nil {
		capacity = map[string]int{}
	}
	return json.Marshal(struct {
		Role     string         `json:"role"`
		Capacity map[string]int `json:"capacity"`
	}{crew.Role, capacity})
}

// String is the role, what the display prints and the --crew flag sends.
func (crew deliveryCrew) String() string { return crew.Role }

// deliveryHealth is a Mission's health, computed by the service from the line; never typed.
type deliveryHealth struct {
	State   string   `json:"state"`
	Reasons []string `json:"reasons"`
	At      int64    `json:"at"`
}

type deliveryUpdate struct {
	At     int64  `json:"at"`
	By     string `json:"by"`
	Health string `json:"health"`
	Text   string `json:"text"`
}

// The triage clock on a new Issue (T-RESP): who must place it and by when.
type deliveryTriage struct {
	Person     string `json:"person"`
	At         int64  `json:"at"`
	By         *int64 `json:"by"`
	StoppedAt  *int64 `json:"stoppedAt"`
	BreachedAt *int64 `json:"breachedAt"`
}

var deliveryHealthLabel = map[string]string{"on_track": "on track", "at_risk": "at risk", "off_track": "off track"}

// healthWord colours a health state for a terminal: green, yellow, red.
func healthWord(p deliveryPalette, health *deliveryHealth) string {
	if health == nil {
		return p.dim + "not measured" + p.reset
	}
	colour := map[string]string{"on_track": p.green, "at_risk": p.yellow, "off_track": p.red}[health.State]
	return colour + deliveryHealthLabel[health.State] + p.reset
}

// parseComponent reads "<repo>:<trunk>[:<calver|semver>]"; the trunk defaults to main.
func parseComponent(value string) (deliveryComponent, error) {
	parts := strings.Split(value, ":")
	component := deliveryComponent{Repo: strings.TrimSpace(parts[0]), Trunk: "main", ReleaseScheme: "calver"}
	if component.Repo == "" {
		return component, errors.New("a Component is <repo>:<trunk>[:<calver|semver>]")
	}
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		component.Trunk = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		switch strings.TrimSpace(parts[2]) {
		case "calver", "semver":
			component.ReleaseScheme = strings.TrimSpace(parts[2])
		default:
			return component, fmt.Errorf("a release scheme is calver or semver, not %q", parts[2])
		}
	}
	return component, nil
}

func deliveryDomainCmd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	switch a.arg(0) {
	case "new":
		return deliveryDomainNew(c, a)
	case "set":
		return deliveryDomainSet(c, a)
	case "show":
		return deliveryDomainShow(c, a)
	}
	return errors.New(`miosa forge domain new "name" --crew <role> [--owner <person>] [--triage <person>] [--component <repo>:<trunk>[:<calver|semver>>]…] [--standing <mission>] | miosa forge domain set <domain> [--owner] [--triage] [--crew] [--standing] | miosa forge domain show [<domain>]`)
}

func deliveryDomainNew(c *deliveryClient, a deliveryArgs) error {
	components := []deliveryComponent{}
	for _, value := range a.repeated("component") {
		component, err := parseComponent(value)
		if err != nil {
			return err
		}
		components = append(components, component)
	}
	var result struct {
		ID string `json:"id"`
	}
	payload := map[string]any{"name": strings.Join(a.rest(1), " "), "owner": optional(a.flag("owner")), "crew": optional(a.flag("crew")), "triage": optional(a.flag("triage")), "components": components, "standing": optional(a.flag("standing")), "words": optional(a.flag("words")), "by": a.actor()}
	if err := c.call("createDomain", payload, &result); err != nil {
		return err
	}
	say(c, "Domain %s. New Issues that match it land in its triage person's Inbox; set them with miosa forge domain set %s --triage <person>", result.ID, result.ID)
	return nil
}

func deliveryDomainSet(c *deliveryClient, a deliveryArgs) error {
	if a.arg(1) == "" {
		return errors.New("miosa forge domain set <domain> [--owner <person>] [--triage <person>] [--crew <role>] [--standing <mission>]")
	}
	payload := map[string]any{"id": a.arg(1), "owner": optional(a.flag("owner")), "triage": optional(a.flag("triage")), "crew": optional(a.flag("crew")), "standing": optional(a.flag("standing")), "by": a.actor()}
	if err := c.call("editDomain", payload, nil); err != nil {
		return err
	}
	say(c, "Domain %s updated.", a.arg(1))
	return nil
}

func deliveryDomainShow(c *deliveryClient, a deliveryArgs) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	p := deliveryColors(c.out)
	shown := 0
	for _, domain := range s.Domains {
		if a.arg(1) != "" && domain.ID != a.arg(1) {
			continue
		}
		shown++
		deliveryDomainLines(c, p, s, domain, a.arg(1) != "")
	}
	if shown == 0 {
		if a.arg(1) != "" {
			return fmt.Errorf("no Domain %s", a.arg(1))
		}
		say(c, "No Domains yet: miosa forge domain new \"Console\" --crew frontend --component MiosaOS:main:calver")
	}
	return nil
}

// deliveryDomainLines prints one Domain: its counts, people, SLA, Components and, when asked for by id, its open Issues.
func deliveryDomainLines(c *deliveryClient, p deliveryPalette, s *deliveryState, domain deliveryDomain, withOrders bool) {
	open, onClock := 0, 0
	for _, order := range s.Orders {
		if order.Domain != domain.ID || order.Dismissed || order.State == "shipped" {
			continue
		}
		open++
		if order.Triage != nil && order.Triage.StoppedAt == nil {
			onClock++
		}
	}
	say(c, "%s%s%s  %s  %s%d open · %d on the triage clock%s", p.bold, domain.ID, p.reset, domain.Name, p.dim, open, onClock, p.reset)
	say(c, "  crew %s · owner %s · triage %s · SLA P0 %dh, P1 %dh", domain.Crew.Role, firstNonEmpty(domain.Owner, "not set"), firstNonEmpty(domain.Triage, "nobody yet"), domain.SLA["P0"], domain.SLA["P1"])
	if domain.Standing != "" {
		say(c, "  standing Mission %s", domain.Standing)
	}
	for _, component := range domain.Components {
		say(c, "  - %s → %s (%s)", component.Repo, component.Trunk, component.ReleaseScheme)
	}
	if !withOrders {
		return
	}
	for _, order := range s.Orders {
		if order.Domain == domain.ID && !order.Dismissed && order.State != "shipped" {
			say(c, "  %s", deliveryRow(p, order))
		}
	}
}

// triageLine says where a new Issue's clock stands: due, overdue or without a rule.
func triageLine(order deliveryOrder, now time.Time) string {
	if order.Triage == nil || order.Triage.StoppedAt != nil {
		return ""
	}
	who := ""
	if order.Triage.Person != "" {
		who = " · " + order.Triage.Person + " on triage"
	}
	if order.Triage.By == nil {
		return "no deadline" + who
	}
	left := time.UnixMilli(*order.Triage.By).Sub(now).Round(time.Minute)
	if left < 0 {
		return fmt.Sprintf("%s overdue%s", (-left).String(), who)
	}
	return fmt.Sprintf("due in %s%s", left.String(), who)
}
