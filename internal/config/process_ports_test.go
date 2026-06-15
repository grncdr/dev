package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseProcessPorts_AbsentReturnsNil(t *testing.T) {
	t.Parallel()
	got, err := ParseProcessPorts(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil when ports absent, got %+v", got)
	}
}

func TestParseProcessPorts_SinglePortShorthand(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		raw  any
		want ProcessPort
	}{
		"random": {raw: "random", want: ProcessPort{Mode: PortModeRandom}},
		"unix":   {raw: "unix", want: ProcessPort{Mode: PortModeUnix}},
		"fixed":  {raw: int64(3000), want: ProcessPort{Mode: PortModeFixed, FixedPort: 3000}},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseProcessPorts(map[string]any{"port": tc.raw})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 port, got %d (%+v)", len(got), got)
			}
			if got[0] != tc.want {
				t.Fatalf("got %+v, want %+v", got[0], tc.want)
			}
		})
	}
}

func TestParseProcessPorts_NamedPortsTable(t *testing.T) {
	t.Parallel()
	got, err := ParseProcessPorts(map[string]any{
		"ports": map[string]any{
			"http":  "random",
			"grpc":  int64(50051),
			"cable": "unix",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ProcessPort{
		{Name: "cable", Mode: PortModeUnix},
		{Name: "grpc", Mode: PortModeFixed, FixedPort: 50051},
		{Name: "http", Mode: PortModeRandom},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseProcessPorts_RejectsPortAndPortsTogether(t *testing.T) {
	t.Parallel()
	_, err := ParseProcessPorts(map[string]any{
		"port":  "random",
		"ports": map[string]any{"http": "random"},
	})
	if err == nil {
		t.Fatalf("expected error when both port and ports set")
	}
}

func TestParseProcessPorts_RejectsEmptyPortsTable(t *testing.T) {
	t.Parallel()
	_, err := ParseProcessPorts(map[string]any{
		"ports": map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error for empty ports table")
	}
}

func TestValidateProcessPorts_MatcherPortRequiredWhenMultiplePorts(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{
		{Name: "http", Mode: PortModeRandom},
		{Name: "grpc", Mode: PortModeRandom},
	}
	proc := map[string]any{
		"proxy": []any{
			map[string]any{"path": "/"},
		},
	}
	if err := ValidateProcessPorts(proc, ports); err == nil {
		t.Fatalf("expected error: matcher missing port when multiple ports declared")
	}
}

func TestValidateProcessPorts_MatcherPortMustReferenceDeclaredName(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{
		{Name: "http", Mode: PortModeRandom},
		{Name: "grpc", Mode: PortModeRandom},
	}
	proc := map[string]any{
		"proxy": []any{
			map[string]any{"path": "/", "port": "websocket"},
		},
	}
	if err := ValidateProcessPorts(proc, ports); err == nil {
		t.Fatalf("expected error: matcher port references unknown name")
	}
}

func TestValidateProcessPorts_SinglePortFormRejectsMatcherPort(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{{Mode: PortModeRandom}}
	proc := map[string]any{
		"port": "random",
		"proxy": []any{
			map[string]any{"path": "/", "port": "http"},
		},
	}
	if err := ValidateProcessPorts(proc, ports); err == nil {
		t.Fatalf("expected error: matcher port set in single-port form")
	}
}

func TestValidateProcessPorts_HealthPortRequiredWhenMultiplePorts(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{
		{Name: "http", Mode: PortModeRandom},
		{Name: "grpc", Mode: PortModeRandom},
	}
	proc := map[string]any{
		"health": map[string]any{"type": "http", "path": "/up"},
	}
	if err := ValidateProcessPorts(proc, ports); err == nil {
		t.Fatalf("expected error: health.port missing when multiple ports declared")
	}
}

func TestValidateProcessPorts_HealthPortMustReferenceDeclaredName(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{
		{Name: "http", Mode: PortModeRandom},
		{Name: "grpc", Mode: PortModeRandom},
	}
	proc := map[string]any{
		"health": map[string]any{"type": "http", "path": "/up", "port": "websocket"},
	}
	if err := ValidateProcessPorts(proc, ports); err == nil {
		t.Fatalf("expected error: health.port references unknown name")
	}
}

func TestValidateProcessPorts_SingleNamedPortAllowsOmittedSelectors(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{{Name: "http", Mode: PortModeRandom}}
	proc := map[string]any{
		"health": map[string]any{"type": "http", "path": "/up"},
		"proxy":  []any{map[string]any{"path": "/"}},
	}
	if err := ValidateProcessPorts(proc, ports); err != nil {
		t.Fatalf("unexpected error when single named port allows implicit selectors: %v", err)
	}
}

func TestValidateProcessPorts_MultiPortValidConfig(t *testing.T) {
	t.Parallel()
	ports := []ProcessPort{
		{Name: "grpc", Mode: PortModeRandom},
		{Name: "http", Mode: PortModeRandom},
	}
	proc := map[string]any{
		"health": map[string]any{"type": "http", "path": "/up", "port": "http"},
		"proxy": []any{
			map[string]any{"path": "/", "port": "http"},
			map[string]any{"path": "/", "port": "grpc"},
		},
	}
	if err := ValidateProcessPorts(proc, ports); err != nil {
		t.Fatalf("unexpected error on valid multi-port config: %v", err)
	}
}

func TestParseProcessPorts_RejectsInvalidPortName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"blank":         " ",
		"leading_digit": "1http",
		"hyphen":        "http-1",
		"uppercase":     "HTTP",
	}
	for name, badName := range cases {
		badName := badName
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseProcessPorts(map[string]any{
				"ports": map[string]any{badName: "random"},
			})
			if err == nil {
				t.Fatalf("expected error for port name %q", badName)
			}
		})
	}
}

func TestLoadProjectConfig_RejectsConflictingPortKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[process.rails]
command = "rails server"
port = "random"

[process.rails.ports]
http = "random"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadProjectConfig(cfgPath)
	if err == nil {
		t.Fatalf("expected LoadProjectConfig to reject both port and ports on same process")
	}
	if !strings.Contains(err.Error(), "rails") {
		t.Fatalf("error should name the offending process: %v", err)
	}
}

func TestLoadProjectConfig_RejectsMatcherWithoutPortInMultiPortForm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[process.rails]
command = "rails server"

[process.rails.ports]
http = "random"
grpc = "random"

[[process.rails.proxy]]
path = "/"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadProjectConfig(cfgPath)
	if err == nil {
		t.Fatalf("expected LoadProjectConfig to reject matcher without port in multi-port form")
	}
}

func TestLoadProjectConfig_AcceptsValidMultiPortConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[process.rails]
command = "rails server"

[process.rails.ports]
http = "random"
grpc = 50051

[process.rails.health]
type = "http"
path = "/up"
port = "http"

[[process.rails.proxy]]
path = "/"
port = "http"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadProjectConfig(cfgPath); err != nil {
		t.Fatalf("unexpected error on valid multi-port config: %v", err)
	}
}
