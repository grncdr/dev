package config

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PortMode is the allocation strategy for a process port: a fixed TCP port,
// a randomly allocated localhost port, or a unix domain socket.
type PortMode int

const (
	PortModeFixed PortMode = iota
	PortModeRandom
	PortModeUnix
)

// ProcessPort describes one listening target declared by a process. The
// single-port `port = ...` shorthand returns one ProcessPort with Name == "".
// The named-ports `[process.X.ports]` table returns one per entry, sorted
// by Name.
type ProcessPort struct {
	Name      string
	Mode      PortMode
	FixedPort int
}

// ParseProcessPorts parses the `port` / `ports` keys on a process config and
// returns the listening targets. Returns nil, nil when neither key is set.
// Returns an error when both keys are set, the ports table is empty, a port
// name is invalid, or a port value cannot be interpreted.
func ParseProcessPorts(proc map[string]any) ([]ProcessPort, error) {
	if proc == nil {
		return nil, nil
	}
	_, hasPort := proc["port"]
	rawPorts, hasPorts := proc["ports"]
	if hasPort && hasPorts {
		return nil, errors.New("`port` and `ports` are mutually exclusive")
	}
	if hasPort {
		port, err := parsePortValue(proc["port"])
		if err != nil {
			return nil, fmt.Errorf("port: %w", err)
		}
		return []ProcessPort{port}, nil
	}
	if !hasPorts {
		return nil, nil
	}
	portsMap, ok := normalizeStringMap(rawPorts)
	if !ok {
		return nil, errors.New("`ports` must be a table mapping names to port specs")
	}
	if len(portsMap) == 0 {
		return nil, errors.New("`ports` must not be empty")
	}
	out := make([]ProcessPort, 0, len(portsMap))
	for rawName, rawValue := range portsMap {
		name := strings.TrimSpace(rawName)
		if !isValidPortName(name) {
			return nil, fmt.Errorf("invalid port name %q (lowercase ASCII letters/digits/underscores, must start with a letter)", rawName)
		}
		port, err := parsePortValue(rawValue)
		if err != nil {
			return nil, fmt.Errorf("ports.%s: %w", name, err)
		}
		port.Name = name
		out = append(out, port)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parsePortValue(raw any) (ProcessPort, error) {
	switch typed := raw.(type) {
	case int:
		return ProcessPort{Mode: PortModeFixed, FixedPort: typed}, nil
	case int64:
		return ProcessPort{Mode: PortModeFixed, FixedPort: int(typed)}, nil
	case float64:
		return ProcessPort{Mode: PortModeFixed, FixedPort: int(typed)}, nil
	case string:
		mode := strings.ToLower(strings.TrimSpace(typed))
		switch mode {
		case "random":
			return ProcessPort{Mode: PortModeRandom}, nil
		case "unix":
			return ProcessPort{Mode: PortModeUnix}, nil
		default:
			parsed, err := strconv.Atoi(mode)
			if err != nil {
				return ProcessPort{}, fmt.Errorf("expected int, \"random\", or \"unix\", got %q", typed)
			}
			return ProcessPort{Mode: PortModeFixed, FixedPort: parsed}, nil
		}
	default:
		return ProcessPort{}, fmt.Errorf("expected int or string, got %T", raw)
	}
}

// ValidateProcessPorts enforces cross-key consistency between the parsed
// `ports` list and the rest of the process config (proxy matchers and
// health). Single-port (`port = ...`) forms reject matcher `port` fields;
// multi-named-port forms require selectors on each matcher and on the
// health block.
func ValidateProcessPorts(proc map[string]any, ports []ProcessPort) error {
	if proc == nil {
		return nil
	}
	singlePort := len(ports) == 1 && ports[0].Name == ""
	multiPort := len(ports) > 1
	names := make(map[string]struct{}, len(ports))
	for _, p := range ports {
		if p.Name != "" {
			names[p.Name] = struct{}{}
		}
	}

	matchers, _ := proc["proxy"].([]any)
	for i, raw := range matchers {
		matcher, ok := normalizeStringMap(raw)
		if !ok {
			continue
		}
		rawPort, hasPort := matcher["port"]
		if !hasPort {
			if multiPort {
				return fmt.Errorf("proxy matcher #%d: `port` is required when multiple ports are declared", i+1)
			}
			continue
		}
		name, ok := rawPort.(string)
		if !ok {
			return fmt.Errorf("proxy matcher #%d: `port` must be a string referencing a name in `ports`", i+1)
		}
		name = strings.TrimSpace(name)
		if singlePort {
			return fmt.Errorf("proxy matcher #%d: `port = %q` not allowed with single-port `port = ...` form", i+1, name)
		}
		if _, ok := names[name]; !ok {
			return fmt.Errorf("proxy matcher #%d: `port = %q` does not match any name in `ports`", i+1, name)
		}
	}

	health, _ := normalizeStringMap(proc["health"])
	if health != nil {
		rawPort, hasPort := health["port"]
		if !hasPort {
			if multiPort {
				return errors.New("`health.port` is required when multiple ports are declared")
			}
		} else if name, ok := rawPort.(string); ok {
			name = strings.TrimSpace(name)
			if name != "" {
				if _, exists := names[name]; !exists && len(names) > 0 {
					return fmt.Errorf("`health.port = %q` does not match any name in `ports`", name)
				}
			}
		}
	}
	return nil
}

func isValidPortName(name string) bool {
	if name == "" {
		return false
	}
	for i, ch := range name {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch == '_':
		case ch >= '0' && ch <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
