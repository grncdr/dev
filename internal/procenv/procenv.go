// Package procenv provides utilities for preparing process execution environments,
// including environment variable handling and command wrapper application.
package procenv

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattn/go-shellwords"
)

// CloneEnv returns a shallow copy of the provided environment map.
func CloneEnv(env map[string]string) map[string]string {
	if env == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

// FormatEnv converts an environment map to a slice of "KEY=value" strings,
// sorted by key for deterministic ordering.
func FormatEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s=%s", key, env[key]))
	}
	return out
}

// ApplyWrapper wraps a command with the given wrapper string.
// The wrapper can contain $COMMAND as a placeholder for where the command should be inserted.
// If $COMMAND is not present, the command is appended to the wrapper arguments.
func ApplyWrapper(wrapper string, command []string) ([]string, error) {
	wrapperArgs, err := shellwords.Parse(wrapper)
	if err != nil {
		return nil, err
	}
	if len(wrapperArgs) == 0 {
		return command, nil
	}

	out := make([]string, 0, len(wrapperArgs)+len(command))
	replaced := false
	for _, arg := range wrapperArgs {
		if strings.Contains(arg, "$COMMAND") {
			replaced = true
			parts := strings.Split(arg, "$COMMAND")
			if len(parts) == 2 {
				if parts[0] != "" {
					out = append(out, parts[0])
				}
				out = append(out, command...)
				if parts[1] != "" {
					out = append(out, parts[1])
				}
			} else {
				out = append(out, command...)
			}
			continue
		}
		out = append(out, arg)
	}
	if !replaced {
		out = append(out, command...)
	}
	return out, nil
}
