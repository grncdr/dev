package daemon

import (
	"reflect"
	"testing"
)

func TestApplyWrapper(t *testing.T) {
	command := []string{"rails", "server"}
	wrapped, err := applyWrapper("bundle exec $COMMAND", command)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(wrapped, []string{"bundle", "exec", "rails", "server"}) {
		t.Fatalf("unexpected wrapper result: %v", wrapped)
	}

	wrapped, err = applyWrapper("docker-compose exec web \"$COMMAND\"", command)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(wrapped, []string{"docker-compose", "exec", "web", "rails", "server"}) {
		t.Fatalf("unexpected quoted wrapper result: %v", wrapped)
	}

	wrapped, err = applyWrapper("devbox run", command)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(wrapped, []string{"devbox", "run", "rails", "server"}) {
		t.Fatalf("unexpected append wrapper result: %v", wrapped)
	}
}
