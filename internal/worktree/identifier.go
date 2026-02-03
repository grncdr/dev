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

var validIdentifierSegmentPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

type ProjectSlug struct {
	Project string
	Slug    string
}

func ParseProjectSlug(input string) (ProjectSlug, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ProjectSlug{}, fmt.Errorf("identifier is required")
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ProjectSlug{}, fmt.Errorf("invalid identifier %q: expected project/slug", input)
	}
	project, err := normalizeIdentifierSegment(parts[0], "project")
	if err != nil {
		return ProjectSlug{}, err
	}
	slug, err := normalizeIdentifierSegment(parts[1], "slug")
	if err != nil {
		return ProjectSlug{}, err
	}
	full := project + "/" + slug
	if len(full) > maxIdentifierLen {
		return ProjectSlug{}, fmt.Errorf("invalid identifier %q: max length is %d", input, maxIdentifierLen)
	}
	return ProjectSlug{Project: project, Slug: slug}, nil
}

func NormalizeIdentifierSegment(segment string) (string, error) {
	return normalizeIdentifierSegment(segment, "segment")
}

func normalizeIdentifierSegment(segment, field string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(segment))
	if normalized == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(normalized) > maxIdentifierSegmentLen {
		return "", fmt.Errorf("invalid %s %q: max length is %d", field, segment, maxIdentifierSegmentLen)
	}
	if !validIdentifierSegmentPattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid %s %q: allowed chars are [a-z0-9_-]", field, segment)
	}
	return normalized, nil
}
