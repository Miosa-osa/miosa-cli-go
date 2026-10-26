package railway

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// MIOSA's limits on a service: a name of 1 to 60 characters and a slug of 1 to
// 40 characters of a-z, 0-9 and "-", starting and ending with a letter or digit.
const (
	maxServiceName = 60
	maxServiceSlug = 40
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// ServiceName turns a Railway service name into one MIOSA accepts: a package
// name such as "@clinic-iq/web" loses its "@" and its "/" becomes "-", and a
// name over 60 characters is shortened with a hash of the original, so the same
// Railway name always gives the same MIOSA name.
func ServiceName(name string) string {
	clean := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(name), "@"))
	clean = strings.ReplaceAll(clean, "/", "-")
	if clean == "" {
		clean = "service"
	}
	return shorten(clean, maxServiceName, name)
}

// ServiceSlug is the slug MIOSA gets for a service name: lower case, a-z 0-9
// and "-", at most 40 characters, shortened with a hash of the name when it is
// longer, so it never fails the slug rule and never changes between runs.
func ServiceSlug(name string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "service"
	}
	return strings.Trim(shorten(slug, maxServiceSlug, name), "-")
}

// shorten cuts s to max characters, ending in "-" and six hex characters of the
// hash of original, when it is longer.
func shorten(s string, max int, original string) string {
	if len(s) <= max {
		return s
	}
	sum := sha256.Sum256([]byte(original))
	tag := hex.EncodeToString(sum[:])[:6]
	head := strings.TrimRight(s[:max-len(tag)-1], "-")
	return head + "-" + tag
}

// sanitizeNames renames every service to a name MIOSA accepts and answers the
// renames (Railway name -> MIOSA name), so references to a renamed service
// follow it.
func sanitizeNames(src Source) (Source, map[string]string) {
	renames := map[string]string{}
	for i := range src.Services {
		svc := &src.Services[i]
		clean := ServiceName(svc.Name)
		if clean != svc.Name {
			renames[svc.Name] = clean
			svc.RailwayName = svc.Name
			svc.Name = clean
		}
	}
	return src, renames
}
