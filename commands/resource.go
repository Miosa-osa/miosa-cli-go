package commands

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A declarative command layer. Most API surfaces are "call one route, show a
// table or a few fields"; an Op describes that in data, so every such command
// shares flag handling, --json, error output, confirmation and tests.

// Arg is a positional argument. It fills the {N} placeholders in Op.Path in
// order (path-escaped).
type Arg struct {
	Name     string
	Optional bool
	// Current makes an omitted optional argument mean "current": the sandbox
	// chosen with 'miosa use'.
	Current bool
	// Complete names a completion source: "sandbox", "profile", or "".
	Complete string
}

// Flag maps a command-line flag to a request body field or query parameter.
type Flag struct {
	Name  string
	Short string
	Usage string
	// Type is string (default), int, bool, strings (repeatable or comma list),
	// or kv (repeatable key=value folded into an object).
	Type    string
	Default string
	// Field is the JSON body field or query name; it defaults to the flag name
	// with dashes turned into underscores. A dotted Field nests objects.
	Field string
	// In is "body" or "query". The default is query for GET and DELETE, body
	// for everything else.
	In       string
	Required bool
	Enum     []string
	// Hidden hides the flag from help.
	Hidden bool
}

// Col is one table column or detail row.
type Col struct {
	Head string
	// Path is a dotted path into the JSON object ("template.name").
	Path string
	// Fmt names a formatter: age, short, bytes, cents, list, count, json.
	Fmt string
}

// Op is one API call exposed as a command.
type Op struct {
	// Use is the cobra Use line: "list", "get <id>", "pause [sandbox]".
	Use     string
	Aliases []string
	Short   string
	Long    string
	Example string

	Method string
	Path   string
	Args   []Arg
	Flags  []Flag

	// Body adjusts the request body after flags are applied. It may return nil
	// to send none.
	Body func(args []string, body map[string]any) (map[string]any, error)
	// Query adjusts the query after flags are applied.
	Query func(args []string, q url.Values) error

	// ListKey is the key holding the rows ("data" by default).
	ListKey string
	Cols    []Col
	Detail  []Col
	// Done is the success line for responses with nothing to show; {N} fills
	// positional args. Use "" to print the response as JSON.
	Done string
	// Confirm, when set, asks for confirmation (or --yes) before sending.
	Confirm string
	// Rows may filter or reorder the rows of a table before it is printed.
	Rows func(cmd *cobra.Command, rows []any) []any
	// Lines is a dotted path to an array printed one element per line in text
	// mode (strings, or objects with "line" or "text").
	Lines  string
	Hidden bool
	// NoAuth skips the signed-in check (public routes).
	NoAuth bool
	// FileBody adds --file: a JSON object read from a file (or - for standard
	// input) that becomes the request body; flags override its fields.
	FileBody bool
	// Before runs after the client is built and before the request is sent; an
	// error stops the command. Used for server-side preview gates.
	Before func(cmd *cobra.Command, c *api.Client) error
	// Idempotent adds --idempotency-key and sends an Idempotency-Key header (a
	// generated one when the flag is not given), for routes that require it.
	Idempotent bool
}

// routeAnnotation records the API route a declarative command calls; the
// generated API map reads it.
const routeAnnotation = "route"

// group builds a parent command from Ops.
func group(use, short, long string, aliases []string, ops ...Op) *cobra.Command {
	parent := &cobra.Command{Use: use, Short: short, Long: long, Aliases: aliases}
	name := strings.Fields(use)[0]
	for _, o := range ops {
		parent.AddCommand(o.command(name))
	}
	return parent
}

func (o Op) command(groupName string) *cobra.Command {
	cmdName := strings.Fields(o.Use)[0]
	cmd := &cobra.Command{
		Annotations: map[string]string{routeAnnotation: o.Method + " " + o.Path},
		Use:         o.Use,
		Aliases:     o.Aliases,
		Short:       o.Short,
		Long:        o.Long,
		Example:     o.Example,
		Hidden:      o.Hidden,
	}
	_ = cmdName
	required, optional := 0, 0
	for _, a := range o.Args {
		if a.Optional {
			optional++
		} else {
			required++
		}
	}
	cmd.Args = cobra.RangeArgs(required, required+optional)
	if len(o.Args) > 0 {
		cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) >= len(o.Args) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeKind(c, o.Args[len(args)].Complete, toComplete)
		}
	}
	yes := false
	flags := cmd.Flags()
	for _, f := range o.Flags {
		bindFlag(cmd, f)
	}
	if o.Confirm != "" {
		flags.BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	}
	if o.Idempotent {
		flags.String("idempotency-key", "", "Replay-safe key: repeating the same key returns the first result (default: generated)")
	}
	if o.FileBody {
		flags.String("file", "", "Read the request body from a JSON file (- for standard input); flags override its fields")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return o.run(cmd, args, yes)
	}
	return cmd
}

func bindFlag(cmd *cobra.Command, f Flag) {
	usage := f.Usage
	if len(f.Enum) > 0 {
		usage += " (" + strings.Join(f.Enum, "|") + ")"
	}
	fs := cmd.Flags()
	switch f.Type {
	case "int":
		d, _ := strconv.Atoi(f.Default)
		fs.IntP(f.Name, f.Short, d, usage)
	case "bool":
		fs.BoolP(f.Name, f.Short, f.Default == "true", usage)
	case "strings", "kv":
		fs.StringArrayP(f.Name, f.Short, nil, usage)
	default:
		fs.StringP(f.Name, f.Short, f.Default, usage)
	}
	if f.Hidden {
		_ = fs.MarkHidden(f.Name)
	}
	if f.Required {
		_ = cmd.MarkFlagRequired(f.Name)
	}
	if len(f.Enum) > 0 {
		enum := f.Enum
		_ = cmd.RegisterFlagCompletionFunc(f.Name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return enum, cobra.ShellCompDirectiveNoFileComp
		})
	}
}

func (f Flag) field() string {
	if f.Field != "" {
		return f.Field
	}
	return strings.ReplaceAll(f.Name, "-", "_")
}

func (o Op) flagIn(f Flag) string {
	if f.In != "" {
		return f.In
	}
	if o.Method == http.MethodGet || o.Method == http.MethodDelete {
		return "query"
	}
	return "body"
}

// fullArgs spreads the given positional args over the declared slots. When
// optional slots come before required ones (<sandbox> <port> with the sandbox
// optional) the given args fill the last slots, so "preview create 8080" and
// "preview create my-box 8080" both work. Missing slots are "".
func (o Op) fullArgs(given []string) []string {
	full := make([]string, len(o.Args))
	leading := false
	seenOptional := false
	for _, a := range o.Args {
		if a.Optional {
			seenOptional = true
		} else if seenOptional {
			leading = true
		}
	}
	if leading {
		copy(full[len(o.Args)-len(given):], given)
	} else {
		copy(full, given)
	}
	return full
}

// pathArgs are the positional values as the API path sees them: a missing
// optional sandbox argument is "current".
func (o Op) pathArgs(given []string) []string {
	full := o.fullArgs(given)
	for i, a := range o.Args {
		if full[i] == "" && a.Current {
			full[i] = "current"
		}
	}
	return full
}

// buildRequest turns args and flags into the request. It does no I/O.
func (o Op) buildRequest(cmd *cobra.Command, args []string) (api.Request, error) {
	path := o.Path
	for i, a := range o.pathArgs(args) {
		if a == "" {
			path = strings.ReplaceAll(path, "/{"+strconv.Itoa(i)+"}", "") // an unset optional segment collapses
			continue
		}
		path = strings.ReplaceAll(path, "{"+strconv.Itoa(i)+"}", url.PathEscape(a))
	}
	q := url.Values{}
	body := map[string]any{}
	if o.FileBody {
		if path, _ := cmd.Flags().GetString("file"); path != "" {
			raw, err := readBodyFile(cmd, path)
			if err != nil {
				return api.Request{}, err
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				return api.Request{}, fmt.Errorf("--file %s is not a JSON object: %w", path, err)
			}
		}
	}
	for _, f := range o.Flags {
		fl := cmd.Flags().Lookup(f.Name)
		if fl == nil {
			continue
		}
		if !fl.Changed && f.Default == "" && f.Type != "bool" {
			continue
		}
		if !fl.Changed && f.Type == "bool" && f.Default != "true" {
			continue
		}
		if len(f.Enum) > 0 && fl.Changed {
			if v := fl.Value.String(); !contains(f.Enum, v) {
				return api.Request{}, fmt.Errorf("--%s must be one of %s (got %q)", f.Name, strings.Join(f.Enum, ", "), v)
			}
		}
		var val any
		switch f.Type {
		case "int":
			n, _ := cmd.Flags().GetInt(f.Name)
			val = n
		case "bool":
			b, _ := cmd.Flags().GetBool(f.Name)
			val = b
		case "strings":
			vals, _ := cmd.Flags().GetStringArray(f.Name)
			var flat []string
			for _, v := range vals {
				for _, p := range strings.Split(v, ",") {
					if p = strings.TrimSpace(p); p != "" {
						flat = append(flat, p)
					}
				}
			}
			val = flat
		case "kv":
			vals, _ := cmd.Flags().GetStringArray(f.Name)
			m := map[string]any{}
			for _, kv := range vals {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return api.Request{}, fmt.Errorf("--%s expects KEY=VALUE (got %q)", f.Name, kv)
				}
				m[k] = v
			}
			val = m
		default:
			val, _ = cmd.Flags().GetString(f.Name)
		}
		if o.flagIn(f) == "query" {
			switch v := val.(type) {
			case []string:
				for _, s := range v {
					q.Add(f.field(), s)
				}
			case map[string]any:
				for k, s := range v {
					q.Add(f.field()+"["+k+"]", fmt.Sprint(s))
				}
			default:
				q.Set(f.field(), fmt.Sprint(v))
			}
		} else {
			setPath(body, f.field(), val)
		}
	}
	if o.Query != nil {
		if err := o.Query(o.pathArgs(args), q); err != nil {
			return api.Request{}, err
		}
	}
	var b any
	if o.Method != http.MethodGet && o.Method != http.MethodDelete || len(body) > 0 {
		m := body
		if o.Body != nil {
			var err error
			if m, err = o.Body(o.pathArgs(args), body); err != nil {
				return api.Request{}, err
			}
		}
		if m != nil {
			b = m
		} else if o.Method == http.MethodPost || o.Method == http.MethodPut || o.Method == http.MethodPatch {
			b = nil
		}
	}
	req := api.Request{Method: o.Method, Path: path, Query: q, Body: b}
	if o.Idempotent {
		key, _ := cmd.Flags().GetString("idempotency-key")
		if key == "" {
			raw := make([]byte, 16)
			if _, err := rand.Read(raw); err != nil {
				return api.Request{}, fmt.Errorf("generating an idempotency key: %w", err)
			}
			key = "cli-" + hex.EncodeToString(raw)
		}
		req.IdempotencyKey = key
	}
	return req, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// setPath stores v at a dotted path ("machine.size") inside m.
func setPath(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

func (o Op) run(cmd *cobra.Command, args []string, yes bool) error {
	req, err := o.buildRequest(cmd, args)
	if err != nil {
		return usagef("%v", err)
	}
	if o.Confirm != "" && !yes {
		if err := confirmDestructive(cmd, expand(o.Confirm, o.pathArgs(args))); err != nil {
			return err
		}
	}
	var c *api.Client
	if o.NoAuth {
		pc, _, err := buildPublicClient()
		if err != nil {
			return die(err)
		}
		c = pc.API
	} else {
		bc, _, err := buildClient()
		if err != nil {
			return die(err)
		}
		c = bc.API
	}
	if o.Before != nil {
		if err := o.Before(cmd, c); err != nil {
			return err
		}
	}
	resp, err := c.JSONAny(cmd.Context(), req)
	if err != nil {
		return die(err)
	}
	return o.render(cmd, o.pathArgs(args), resp)
}

var unfilledArg = regexp.MustCompile(`\{\d+\}`)

func expand(tmpl string, args []string) string {
	for i, a := range args {
		tmpl = strings.ReplaceAll(tmpl, "{"+strconv.Itoa(i)+"}", a)
	}
	// An omitted optional argument stood for the current sandbox.
	return unfilledArg.ReplaceAllString(tmpl, "current")
}

// confirmDestructive asks on a terminal and refuses off one.
func confirmDestructive(cmd *cobra.Command, what string) error {
	in := cmd.InOrStdin()
	f, isFile := in.(*os.File)
	if isFile && term.IsTerminal(int(f.Fd())) {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", what)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
			return nil
		}
		return die(fmt.Errorf("canceled"))
	}
	return usagef("%s - pass --yes to confirm", what)
}

func (o Op) render(cmd *cobra.Command, args []string, resp any) error {
	p := printerFor(cmd)
	if isJSON() {
		if resp == nil {
			return p.JSON(map[string]any{"ok": true})
		}
		return p.JSON(resp)
	}
	switch {
	case o.Lines != "":
		items, _ := lookupPath(resp, o.Lines).([]any)
		for _, it := range items {
			switch v := it.(type) {
			case string:
				fmt.Fprintln(p.Writer(), v)
			case map[string]any:
				if l, ok := v["line"].(string); ok {
					fmt.Fprintln(p.Writer(), l)
				} else if l, ok := v["text"].(string); ok {
					fmt.Fprintln(p.Writer(), l)
				} else {
					b, _ := json.Marshal(v)
					fmt.Fprintln(p.Writer(), string(b))
				}
			}
		}
	case len(o.Cols) > 0:
		rows, _ := findRows(resp, o.ListKey)
		if o.Rows != nil {
			rows = o.Rows(cmd, rows)
		}
		if len(rows) == 0 {
			p.Line("No results.")
			return nil
		}
		heads := make([]string, len(o.Cols))
		for i, c := range o.Cols {
			heads[i] = c.Head
		}
		table := make([][]string, 0, len(rows))
		for _, r := range rows {
			line := make([]string, len(o.Cols))
			for i, c := range o.Cols {
				line[i] = formatValue(lookupPath(r, c.Path), c.Fmt)
			}
			table = append(table, line)
		}
		p.Table(heads, table)
	case len(o.Detail) > 0:
		obj := unwrapObject(resp)
		rows := make([][2]string, 0, len(o.Detail))
		shown := false
		for _, c := range o.Detail {
			v := formatValue(lookupPath(obj, c.Path), c.Fmt)
			shown = shown || v != ""
			rows = append(rows, [2]string{c.Head, v})
		}
		if !shown && resp != nil {
			// Nothing we know how to show: print what the server sent.
			return p.JSON(resp)
		}
		p.Fields(rows)
	case o.Done != "":
		p.Success("%s", expand(o.Done, args))
	case resp == nil:
		p.Success("done")
	default:
		return p.JSON(resp)
	}
	return nil
}

// findRows returns the list in a response: the top-level array, or the array
// under key (default "data"), or the first array value in the object.
func findRows(resp any, key string) ([]any, bool) {
	switch v := resp.(type) {
	case []any:
		return v, true
	case map[string]any:
		if key == "" {
			key = "data"
		}
		if rows, ok := v[key].([]any); ok {
			return rows, true
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if rows, ok := v[k].([]any); ok {
				return rows, true
			}
		}
	}
	return nil, false
}

// unwrapObject returns resp, or resp["data"] when that is an object.
func unwrapObject(resp any) any {
	if m, ok := resp.(map[string]any); ok {
		if d, ok := m["data"].(map[string]any); ok {
			return d
		}
		// {"host": {...}}: a single object under a name of its own.
		if len(m) == 1 {
			for _, v := range m {
				if d, ok := v.(map[string]any); ok {
					return d
				}
			}
		}
	}
	return resp
}

func lookupPath(v any, path string) any {
	for _, p := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func formatValue(v any, format string) string {
	if v == nil {
		return ""
	}
	switch format {
	case "age":
		if s, ok := v.(string); ok {
			return formatAge(s)
		}
	case "short":
		if s, ok := v.(string); ok && len(s) > 8 {
			return s[:8]
		}
	case "bytes":
		if n, ok := toFloat(v); ok {
			return humanBytes(n)
		}
	case "cents":
		if n, ok := toFloat(v); ok {
			return fmt.Sprintf("$%.2f", n/100)
		}
	case "count":
		if l, ok := v.([]any); ok {
			return strconv.Itoa(len(l))
		}
	case "list":
		if l, ok := v.([]any); ok {
			parts := make([]string, 0, len(l))
			for _, e := range l {
				parts = append(parts, fmt.Sprint(e))
			}
			return strings.Join(parts, ",")
		}
	case "json":
		b, _ := json.Marshal(v)
		return string(b)
	case "hours":
		if n, ok := toFloat(v); ok {
			return strconv.FormatFloat(n/3600, 'f', 1, 64)
		}
	case "clip":
		if s, ok := v.(string); ok {
			return clipText(s, 60)
		}
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return trimFloat(t.String())
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		if len(t) == 0 {
			return ""
		}
		var parts []string
		for _, e := range t {
			parts = append(parts, formatValue(e, ""))
		}
		return strings.Join(parts, ",")
	default:
		b, _ := json.Marshal(v)
		s := string(b)
		if len(s) > 48 {
			s = s[:45] + "..."
		}
		return s
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func humanBytes(n float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", int(n))
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

// completeKind offers completions for an argument source.
func completeKind(cmd *cobra.Command, kind, prefix string) ([]string, cobra.ShellCompDirective) {
	switch kind {
	case "sandbox":
		names := cachedSandboxNames(cmd.Context())
		var out []string
		for _, n := range names {
			if strings.HasPrefix(n, prefix) {
				out = append(out, n)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	case "profile":
		return profileNames(prefix), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func formatBytes(n int64) string { return humanBytes(float64(n)) }

// trimFloat shortens a decimal to at most 4 places ("15.518699999999999" ->
// "15.5187"); integers pass through.
func trimFloat(s string) string {
	i := strings.IndexByte(s, '.')
	if i < 0 || len(s)-i-1 <= 4 || strings.ContainsAny(s, "eE") {
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	out := strconv.FormatFloat(f, 'f', 4, 64)
	out = strings.TrimRight(out, "0")
	return strings.TrimSuffix(out, ".")
}

// reqPost is a JSON POST request.
func reqPost(path string, body any) api.Request {
	return api.Request{Method: http.MethodPost, Path: path, Body: body}
}

// readBodyFile reads a --file argument: a path, or - for standard input.
func readBodyFile(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading --file: %w", err)
	}
	return b, nil
}
