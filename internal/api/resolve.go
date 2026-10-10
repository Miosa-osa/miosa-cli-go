package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a canonical UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// sandboxLiterals are path segments under /sandboxes/ that are routes, not ids.
var sandboxLiterals = map[string]bool{"": true, "batch": true, "batches": true, "by-name": true, "run": true}

// Resolver maps sandbox references (names and aliases) to ids and rewrites
// request paths so every command accepts a name wherever an id is allowed.
type Resolver struct {
	// Aliases maps reserved words to ids: "current" and "self".
	Aliases map[string]string

	mu    sync.Mutex
	cache map[string]string
}

// ResolveSandbox returns the id for ref: a UUID, an alias, or a sandbox name.
// A ref that is no UUID and names no sandbox is returned unchanged, so the
// server decides (it answers 400 INVALID_ID, which the CLI explains).
func (c *Client) ResolveSandbox(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("no sandbox specified")
	}
	if IsUUID(ref) {
		return ref, nil
	}
	r := c.Resolver
	if r == nil {
		r = &Resolver{}
		c.Resolver = r
	}
	if v, ok := r.Aliases[ref]; ok {
		if v == "" {
			return "", fmt.Errorf("%q is not set (run 'miosa use <name>' or pass an id)", ref)
		}
		ref = v
		if IsUUID(ref) {
			return ref, nil
		}
	}
	r.mu.Lock()
	if id, ok := r.cache[ref]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()

	var out struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/sandboxes/by-name/" + url.PathEscape(ref)}, &out)
	if err != nil {
		if IsStatus(err, http.StatusNotFound) {
			return ref, nil
		}
		return "", err
	}
	id := out.ID
	if id == "" {
		id = out.Data.ID
	}
	if id == "" {
		return ref, nil
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]string{}
	}
	r.cache[ref] = id
	r.mu.Unlock()
	return id, nil
}

// ResolveComputer returns the id for a computer reference: a UUID or a name
// (computers have no by-name route, so the list is searched).
func (c *Client) ResolveComputer(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("no computer specified")
	}
	if IsUUID(ref) {
		return ref, nil
	}
	r := c.Resolver
	if r == nil {
		r = &Resolver{}
		c.Resolver = r
	}
	key := "computer:" + ref
	r.mu.Lock()
	if id, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()
	type row struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var out struct {
		Data      []row `json:"data"`
		Computers []row `json:"computers"` // the list route answers with this key
	}
	if err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/computers"}, &out); err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return ref, nil // the list is unavailable to this key: let the server judge the reference
		}
		return "", err
	}
	for _, comp := range append(out.Data, out.Computers...) {
		if comp.Name == ref {
			r.mu.Lock()
			if r.cache == nil {
				r.cache = map[string]string{}
			}
			r.cache[key] = comp.ID
			r.mu.Unlock()
			return comp.ID, nil
		}
	}
	return ref, nil // unknown: let the server answer
}

// agentLiterals are /agents/<segment> routes that are not agent ids.
var agentLiterals = map[string]bool{"": true, "harnesses": true, "harness-versions": true, "templates": true, "defaults": true, "chats": true}

// ResolveAgent returns the id for an agent reference: a UUID or an agent name.
func (c *Client) ResolveAgent(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("no agent specified")
	}
	if IsUUID(ref) {
		return ref, nil
	}
	r := c.Resolver
	if r == nil {
		r = &Resolver{}
		c.Resolver = r
	}
	key := "agent:" + ref
	r.mu.Lock()
	if id, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("limit", "100")
	if err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/agents", Query: q}, &out); err != nil {
		return "", err
	}
	for _, a := range out.Data {
		if a.Name == ref {
			r.mu.Lock()
			if r.cache == nil {
				r.cache = map[string]string{}
			}
			r.cache[key] = a.ID
			r.mu.Unlock()
			return a.ID, nil
		}
	}
	return "", &Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: fmt.Sprintf("no agent named %q (see 'miosa agent list')", ref)}
}

// namedResource describes a collection whose members can be addressed by a
// name or slug in the path: /<collection>/<ref>/...
type namedResource struct {
	collection string   // "deployments"
	list       string   // path of the list route
	literals   []string // path segments that are routes, not references
	label      string   // for errors: "deployment"
}

var namedResources = []namedResource{
	{collection: "workspaces", list: "/workspaces", label: "workspace"},
	{collection: "deployments", list: "/deployments", literals: []string{"drop"}, label: "deployment"},
	{collection: "databases", list: "/databases", label: "database"},
	{collection: "projects", list: "/projects", label: "project"},
	{collection: "workflows", list: "/workflows", literals: []string{"capabilities", "node-types", "templates", "usage", "runs", "trigger"}, label: "workflow"},
	{collection: "volumes", list: "/volumes", label: "volume"},
	{collection: "cron-jobs", list: "/cron-jobs", label: "cron job"},
	{collection: "functions", list: "/functions", label: "function"},
	{collection: "forge/repositories", list: "/forge/repositories", literals: []string{"by-slug"}, label: "repository"},
}

// ResolveNamed returns the id of the member of res called ref (by name or
// slug). A reference that matches nothing is returned unchanged so the server
// answers for it.
func (c *Client) resolveNamed(ctx context.Context, res namedResource, ref string) (string, error) {
	r := c.Resolver
	if r == nil {
		r = &Resolver{}
		c.Resolver = r
	}
	key := res.collection + ":" + ref
	r.mu.Lock()
	if id, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("limit", "100")
	if err := c.JSON(ctx, Request{Method: http.MethodGet, Path: res.list, Query: q}, &out); err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return ref, nil
		}
		return "", err
	}
	for _, row := range out.Data {
		if row.Slug == ref || row.Name == ref {
			r.mu.Lock()
			if r.cache == nil {
				r.cache = map[string]string{}
			}
			r.cache[key] = row.ID
			r.mu.Unlock()
			return row.ID, nil
		}
	}
	return ref, nil
}

// ResolveWorkspace returns the id of a workspace given its slug, name or id.
func (c *Client) ResolveWorkspace(ctx context.Context, ref string) (string, error) {
	if IsUUID(ref) {
		return ref, nil
	}
	return c.resolveNamed(ctx, namedResources[0], ref)
}

func (c *Client) rewriteNamed(ctx context.Context, path string) (string, bool, error) {
	for _, res := range namedResources {
		prefix := "/" + res.collection + "/"
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		end := strings.IndexAny(rest, "/?")
		seg, tail := rest, ""
		if end >= 0 {
			seg, tail = rest[:end], rest[end:]
		}
		if seg == "" || IsUUID(seg) {
			return path, true, nil
		}
		for _, lit := range res.literals {
			if seg == lit {
				return path, true, nil
			}
		}
		if dec, err := url.PathUnescape(seg); err == nil {
			seg = dec
		}
		id, err := c.resolveNamed(ctx, res, seg)
		if err != nil {
			return "", true, err
		}
		return prefix + id + tail, true, nil
	}
	return path, false, nil
}

// RewritePath replaces a sandbox or computer name (or the "current" and
// "self" aliases) in /sandboxes/<ref>/... and /computers/<ref>/... with the
// id. Paths that do not address one machine pass through.
func (c *Client) RewritePath(ctx context.Context, path string) (string, error) {
	if p, handled, err := c.rewriteNamed(ctx, path); handled {
		return p, err
	}
	if strings.HasPrefix(path, "/agents/") {
		rest := path[len("/agents/"):]
		end := strings.IndexAny(rest, "/?")
		seg, tail := rest, ""
		if end >= 0 {
			seg, tail = rest[:end], rest[end:]
		}
		if agentLiterals[seg] || IsUUID(seg) {
			return path, nil
		}
		if dec, err := url.PathUnescape(seg); err == nil {
			seg = dec
		}
		id, err := c.ResolveAgent(ctx, seg)
		if err != nil {
			return "", err
		}
		return "/agents/" + id + tail, nil
	}
	if strings.HasPrefix(path, "/computers/") {
		rest := path[len("/computers/"):]
		end := strings.IndexAny(rest, "/?")
		seg, tail := rest, ""
		if end >= 0 {
			seg, tail = rest[:end], rest[end:]
		}
		if seg == "" || IsUUID(seg) {
			return path, nil
		}
		if dec, err := url.PathUnescape(seg); err == nil {
			seg = dec
		}
		id, err := c.ResolveComputer(ctx, seg)
		if err != nil {
			return "", err
		}
		return "/computers/" + id + tail, nil
	}
	const prefix = "/sandboxes/"
	if !strings.HasPrefix(path, prefix) {
		return path, nil
	}
	rest := path[len(prefix):]
	end := strings.IndexAny(rest, "/?")
	seg, tail := rest, ""
	if end >= 0 {
		seg, tail = rest[:end], rest[end:]
	}
	if sandboxLiterals[seg] || IsUUID(seg) {
		return path, nil
	}
	if dec, err := url.PathUnescape(seg); err == nil {
		seg = dec
	}
	id, err := c.ResolveSandbox(ctx, seg)
	if err != nil {
		return "", err
	}
	return prefix + id + tail, nil
}
