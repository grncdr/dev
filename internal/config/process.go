package config

import (
	"fmt"
	"sort"
	"strings"
)

// ParseProcessNeeds normalizes the `needs` field into a de-duplicated list of process names.
// Accepts a single string or an array of strings.
func ParseProcessNeeds(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var values []string
	switch typed := raw.(type) {
	case string:
		values = []string{typed}
	case []string:
		values = append(values, typed...)
	case []any:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			val, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("process needs must be strings")
			}
			values = append(values, val)
		}
	default:
		return nil, fmt.Errorf("process needs must be a string or array")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, val := range values {
		name := strings.TrimSpace(val)
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
