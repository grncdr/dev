package worktree

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxIdentifierSegmentLen = 63
	maxIdentifierLen        = 127
)

var (
	validProjectPattern = regexp.MustCompile(`^[a-z0-9/_-]+$`)
	validSlugPattern2   = regexp.MustCompile(`^[a-z0-9/_-]+$`)
)

// Identifier is a parsed "project:slug" worktree identifier. It is the single
// representation of worktree identity: other identifier structs (e.g.
// ProcessIdentifier) embed it rather than repeating Project/Slug fields.
type Identifier struct {
	Project string `json:"project,omitempty"`
	Slug    string `json:"slug,omitempty"`
}

func ParseIdentifier(input string) (Identifier, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return Identifier{}, fmt.Errorf("identifier is required")
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Identifier{}, fmt.Errorf("invalid identifier %q: expected project:slug", input)
	}
	project, err := normalizeProject(parts[0])
	if err != nil {
		return Identifier{}, err
	}
	slug, err := normalizeSlug(parts[1])
	if err != nil {
		return Identifier{}, err
	}
	full := project + ":" + slug
	if len(full) > maxIdentifierLen {
		return Identifier{}, fmt.Errorf("invalid identifier %q: max length is %d", input, maxIdentifierLen)
	}
	return Identifier{Project: project, Slug: slug}, nil
}

// String renders the identity as the canonical "project:slug" form.
func (p Identifier) String() string {
	return p.Project + ":" + p.Slug
}

// Equal reports whether p and other identify the same worktree. A slug alone is
// not unique across projects — every project has a "main" — so both segments
// must match. Comparison is case-insensitive and whitespace-trimmed.
func (p Identifier) Equal(other Identifier) bool {
	return strings.EqualFold(strings.TrimSpace(p.Project), strings.TrimSpace(other.Project)) &&
		strings.EqualFold(strings.TrimSpace(p.Slug), strings.TrimSpace(other.Slug))
}

// SlugDNSLabel returns the DNS label for a slug (the last path segment).
// Example: "feature/branch" → "branch"
func SlugDNSLabel(slug string) string {
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		return slug[i+1:]
	}
	return slug
}

// ProcessIdentifier represents a parsed process identifier: a worktree
// Identifier plus a process name.
type ProcessIdentifier struct {
	Identifier
	Process string
}

// String renders the fully-qualified "project:slug:process" form, omitting any
// empty leading segments. It shadows the embedded Identifier.String so the
// process segment is not dropped.
func (p ProcessIdentifier) String() string {
	return FormatProcessIdentifier(p.Project, p.Slug, p.Process)
}

// FormatProcessIdentifier formats a fully-qualified process identifier.
// Empty leading segments are omitted so callers can still use it with
// partially-resolved identifiers.
func FormatProcessIdentifier(project, slug, process string) string {
	parts := make([]string, 0, 3)
	if project = strings.TrimSpace(project); project != "" {
		parts = append(parts, project)
	}
	if slug = strings.TrimSpace(slug); slug != "" {
		parts = append(parts, slug)
	}
	if process = strings.TrimSpace(process); process != "" {
		parts = append(parts, process)
	}
	return strings.Join(parts, ":")
}

// ParseProcessIdentifier parses process identifiers in format:
// - "process"
// - "slug:process"
// - "project:slug:process"
// The slug can contain slashes (e.g., "feature/branch").
func ParseProcessIdentifier(input string) (ProcessIdentifier, error) {
	if input == "" {
		return ProcessIdentifier{}, fmt.Errorf("target is required")
	}
	// Split from the right to find the process (last segment after :)
	lastColon := strings.LastIndex(input, ":")
	if lastColon == -1 {
		// Just a process name
		return ProcessIdentifier{Process: input}, nil
	}
	if lastColon == len(input)-1 {
		return ProcessIdentifier{}, fmt.Errorf("process is required after ':'")
	}
	process := input[lastColon+1:]
	left := input[:lastColon]
	if left == "" {
		return ProcessIdentifier{}, fmt.Errorf("slug is required before ':'")
	}
	// Check if there's another colon (project:slug)
	firstColon := strings.Index(left, ":")
	if firstColon == -1 {
		// slug:process
		return ProcessIdentifier{Identifier: Identifier{Slug: left}, Process: process}, nil
	}
	if firstColon == 0 {
		return ProcessIdentifier{}, fmt.Errorf("project is required before ':'")
	}
	if firstColon == len(left)-1 {
		return ProcessIdentifier{}, fmt.Errorf("slug is required after project")
	}
	project := left[:firstColon]
	slug := left[firstColon+1:]
	return ProcessIdentifier{Identifier: Identifier{Project: project, Slug: slug}, Process: process}, nil
}

func normalizeProject(segment string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(segment))
	if normalized == "" {
		return "", fmt.Errorf("project is required")
	}
	if len(normalized) > maxIdentifierSegmentLen {
		return "", fmt.Errorf("invalid project %q: max length is %d", segment, maxIdentifierSegmentLen)
	}
	if !validProjectPattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid project %q: allowed chars are [a-z0-9/_-]", segment)
	}
	return normalized, nil
}

func normalizeSlug(segment string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(segment))
	if normalized == "" {
		return "", fmt.Errorf("slug is required")
	}
	if len(normalized) > maxIdentifierSegmentLen {
		return "", fmt.Errorf("invalid slug %q: max length is %d", segment, maxIdentifierSegmentLen)
	}
	if !validSlugPattern2.MatchString(normalized) {
		return "", fmt.Errorf("invalid slug %q: allowed chars are [a-z0-9/_-]", segment)
	}
	return normalized, nil
}

func NormalizeIdentifierSegment(segment string) (string, error) {
	return normalizeProject(segment)
}
