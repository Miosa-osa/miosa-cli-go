package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Relations between Issues and customers' requests (report "Linear's architecture vs MIOSA
// Delivery", section 4: RELATIONS WO-371, CUSTOMERS WO-374). Agents write these; forge-local
// keeps every write idempotent, so a verb run twice changes nothing the second time.

// deliveryRequest is one customer's request on an Issue.
type deliveryRequest struct {
	Customer  string `json:"customer"`
	Source    string `json:"source"`
	Important bool   `json:"important"`
	At        int64  `json:"at"`
	Note      string `json:"note"`
}

// deliveryCustomer is a customer whose requests sit on Issues.
type deliveryCustomer struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Domain *string `json:"domain"`
	Tier   *string `json:"tier"`
	Owner  *string `json:"owner"`
}

func (s *deliveryState) customer(id string) *deliveryCustomer {
	for i := range s.Customers {
		if s.Customers[i].ID == id {
			return &s.Customers[i]
		}
	}
	return nil
}

func deliveryRelationCommands() []deliveryCommand {
	return []deliveryCommand{
		{"block WO-12 --by WO-9", "WO-12 waits on WO-9; it clears when WO-9 merges", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if a.arg(0) == "" || a.flag("by") == "" {
				return errors.New("usage: miosa forge block WO-12 --by WO-9")
			}
			var result struct {
				BlockedBy []int `json:"blockedBy"`
			}
			if err := c.call("block", map[string]any{"id": woNumber(a.arg(0)), "blocker": woNumber(a.flag("by")), "by": a.actor()}, &result); err != nil {
				return err
			}
			say(c, "%s waits on %s.", woLabel(woNumber(a.arg(0)).(int)), woLabels(result.BlockedBy))
			return nil
		}},
		{"duplicate WO-12 --of WO-9", "WO-12 is a second report of WO-9: its attachments, requests and note move there", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if a.arg(0) == "" || a.flag("of") == "" {
				return errors.New("usage: miosa forge duplicate WO-12 --of WO-9")
			}
			var result struct {
				Canonical int `json:"canonical"`
				Moved     struct {
					Attachments int `json:"attachments"`
					Requests    int `json:"requests"`
				} `json:"moved"`
			}
			if err := c.call("duplicate", map[string]any{"id": woNumber(a.arg(0)), "of": woNumber(a.flag("of")), "by": a.actor()}, &result); err != nil {
				return err
			}
			say(c, "%s is a duplicate of %s: moved %s and %s.", woLabel(woNumber(a.arg(0)).(int)), woLabel(result.Canonical), plural(result.Moved.Attachments, "attachment"), plural(result.Moved.Requests, "request"))
			return nil
		}},
		{"split WO-12 \"child\" \"child\"…", "Split an Issue into children; they inherit Mission, phase, Domain and priority", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if a.arg(0) == "" || len(a.rest(1)) == 0 {
				return errors.New("usage: miosa forge split WO-12 \"first child\" \"second child\"")
			}
			var result struct {
				Children []int `json:"children"`
				Created  []int `json:"created"`
			}
			if err := c.call("split", map[string]any{"id": woNumber(a.arg(0)), "titles": a.rest(1), "by": firstNonEmpty(a.flag("as"), a.runner(), a.actor())}, &result); err != nil {
				return err
			}
			label := woLabel(woNumber(a.arg(0)).(int))
			if len(result.Created) == 0 {
				say(c, "%s already has these children: %s.", label, woLabels(result.Children))
				return nil
			}
			say(c, "Split %s into %s. Draft each child's spec next; %s ships when its last child does.", label, woLabels(result.Created), label)
			return nil
		}},
		{"request WO-12 --customer acme", "A customer asked for this Issue (--important when they said so)", func(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
			if a.arg(0) == "" || a.flag("customer") == "" {
				return errors.New("usage: miosa forge request WO-12 --customer acme [--important] [--source email] [--note \"…\"]")
			}
			var result struct {
				Added    bool   `json:"added"`
				Customer string `json:"customer"`
				Requests int    `json:"requests"`
			}
			if err := c.call("request", map[string]any{"id": woNumber(a.arg(0)), "customer": a.flag("customer"), "important": a.has("important"), "source": a.flag("source"), "note": a.flag("note"), "by": a.actor()}, &result); err != nil {
				return err
			}
			label := woLabel(woNumber(a.arg(0)).(int))
			if !result.Added {
				say(c, "%s already asked for %s (%s on it).", result.Customer, label, plural(result.Requests, "request"))
				return nil
			}
			say(c, "%s asked for %s: %s on it.", result.Customer, label, plural(result.Requests, "request"))
			return nil
		}},
		{"customer [id]", "Customers: list them, or create or edit one (--name, --domain, --tier, --owner)", deliveryCustomerCmd},
	}
}

func deliveryCustomerCmd(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	edits := map[string]any{}
	for _, key := range []string{"name", "domain", "tier", "owner"} {
		if a.has(key) {
			edits[key] = a.flag(key)
		}
	}
	if a.arg(0) != "" && len(edits) > 0 {
		var customer deliveryCustomer
		edits["id"] = a.arg(0)
		edits["by"] = a.actor()
		if err := c.call("customer", edits, &customer); err != nil {
			return err
		}
		say(c, "%s", customerLine(customer))
		return nil
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	shown := 0
	for _, customer := range s.Customers {
		if a.arg(0) != "" && customer.ID != a.arg(0) {
			continue
		}
		shown++
		say(c, "%s", customerLine(customer))
		for _, order := range s.Orders {
			for _, request := range order.Requests {
				if request.Customer == customer.ID {
					mark := ""
					if request.Important {
						mark = " · important"
					}
					say(c, "  %s %s (%s)%s", woLabel(order.ID), order.Title, deliveryStateLabel[order.State], mark)
					break
				}
			}
		}
	}
	if shown == 0 {
		if a.arg(0) != "" {
			return fmt.Errorf("no customer %s; create one: miosa forge customer %s --name \"…\"", a.arg(0), a.arg(0))
		}
		say(c, "No customers yet. One appears with its first request: miosa forge request WO-12 --customer acme")
	}
	return nil
}

func customerLine(customer deliveryCustomer) string {
	parts := []string{customer.ID, customer.Name}
	for _, field := range []*string{customer.Domain, customer.Tier, customer.Owner} {
		if field != nil && *field != "" {
			parts = append(parts, *field)
		}
	}
	return strings.Join(parts, "  ")
}

// relationLines is what `show` prints under an Issue: each relation that exists, one line.
func relationLines(s *deliveryState, order *deliveryOrder) []string {
	var lines []string
	if order.DuplicateOf != nil {
		lines = append(lines, fmt.Sprintf("  duplicate of %s", woLabel(*order.DuplicateOf)))
	}
	if order.Parent != nil {
		lines = append(lines, fmt.Sprintf("  parent %s (ships when its last child does)", woLabel(*order.Parent)))
	}
	if len(order.BlockedBy) > 0 {
		lines = append(lines, "  blocked by "+woLabels(order.BlockedBy))
	}
	var blocking, children, duplicates []int
	for _, other := range s.Orders {
		if other.Parent != nil && *other.Parent == order.ID && !other.Dismissed {
			children = append(children, other.ID)
		}
		if other.DuplicateOf != nil && *other.DuplicateOf == order.ID {
			duplicates = append(duplicates, other.ID)
		}
		if other.Dismissed {
			continue
		}
		for _, id := range other.BlockedBy {
			if id == order.ID {
				blocking = append(blocking, other.ID)
			}
		}
	}
	if len(blocking) > 0 {
		lines = append(lines, "  blocking "+woLabels(blocking))
	}
	if len(order.Related) > 0 {
		lines = append(lines, "  related "+woLabels(order.Related))
	}
	if len(duplicates) > 0 {
		lines = append(lines, "  duplicates "+woLabels(duplicates))
	}
	if len(children) > 0 {
		shipped := 0
		for _, id := range children {
			if child := s.order(woLabel(id)); child != nil && child.State == "shipped" {
				shipped++
			}
		}
		lines = append(lines, fmt.Sprintf("  children %s (%d/%d shipped)", woLabels(children), shipped, len(children)))
	}
	return lines
}

// requestLines is what `show` prints for the customers who asked.
func requestLines(s *deliveryState, order *deliveryOrder) []string {
	var lines []string
	for _, request := range order.Requests {
		name := request.Customer
		if customer := s.customer(request.Customer); customer != nil {
			name = customer.Name
		}
		line := "  " + name
		if request.Important {
			line += " · important"
		}
		if request.Source != "" {
			line += " · " + request.Source
		}
		if request.Note != "" {
			line += fmt.Sprintf(" · %q", request.Note)
		}
		lines = append(lines, line)
	}
	return lines
}
