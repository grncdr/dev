package procenv

import (
	"reflect"
	"testing"
)

func TestCloneEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input map[string]string
		want  map[string]string
	}{
		{
			name:  "nil map",
			input: nil,
			want:  map[string]string{},
		},
		{
			name:  "empty map",
			input: map[string]string{},
			want:  map[string]string{},
		},
		{
			name:  "single entry",
			input: map[string]string{"FOO": "bar"},
			want:  map[string]string{"FOO": "bar"},
		},
		{
			name:  "multiple entries",
			input: map[string]string{"A": "1", "B": "2", "C": "3"},
			want:  map[string]string{"A": "1", "B": "2", "C": "3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CloneEnv(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CloneEnv() = %v, want %v", got, tt.want)
			}
			// Verify it's a copy, not the same map
			if tt.input != nil && len(tt.input) > 0 {
				got["MODIFIED"] = "true"
				if _, exists := tt.input["MODIFIED"]; exists {
					t.Error("CloneEnv() returned same map, not a copy")
				}
			}
		})
	}
}

func TestFormatEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input map[string]string
		want  []string
	}{
		{
			name:  "nil map",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty map",
			input: map[string]string{},
			want:  nil,
		},
		{
			name:  "single entry",
			input: map[string]string{"FOO": "bar"},
			want:  []string{"FOO=bar"},
		},
		{
			name:  "multiple entries sorted",
			input: map[string]string{"Z": "last", "A": "first", "M": "middle"},
			want:  []string{"A=first", "M=middle", "Z=last"},
		},
		{
			name:  "values with equals sign",
			input: map[string]string{"KEY": "a=b=c"},
			want:  []string{"KEY=a=b=c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatEnv(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyWrapper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wrapper string
		command []string
		want    []string
		wantErr bool
	}{
		{
			name:    "empty wrapper",
			wrapper: "",
			command: []string{"echo", "hello"},
			want:    []string{"echo", "hello"},
		},
		{
			name:    "simple wrapper without placeholder",
			wrapper: "sudo",
			command: []string{"rm", "-rf", "/tmp/foo"},
			want:    []string{"sudo", "rm", "-rf", "/tmp/foo"},
		},
		{
			name:    "wrapper with $COMMAND placeholder",
			wrapper: "bash -c $COMMAND",
			command: []string{"echo", "hello"},
			want:    []string{"bash", "-c", "echo", "hello"},
		},
		{
			name:    "wrapper with multiple args",
			wrapper: "env FOO=bar",
			command: []string{"myapp"},
			want:    []string{"env", "FOO=bar", "myapp"},
		},
		{
			name:    "complex wrapper",
			wrapper: "direnv exec /path",
			command: []string{"bundle", "exec", "rails", "server"},
			want:    []string{"direnv", "exec", "/path", "bundle", "exec", "rails", "server"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyWrapper(tt.wrapper, tt.command)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyWrapper() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyWrapper() = %v, want %v", got, tt.want)
			}
		})
	}
}
