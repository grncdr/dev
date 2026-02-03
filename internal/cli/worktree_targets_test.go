package cli

import "testing"

func TestResolveProcessTargetsExplicit(t *testing.T) {
	targets, err := resolveProcessTargets([]string{
		"proj:feature:rails",
		"proj:feature:webpack",
		"proj:other:*",
	})
	if err != nil {
		t.Fatalf("resolveProcessTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(targets))
	}
	feature := targets["feature"]
	if feature == nil || feature.all {
		t.Fatalf("expected specific processes for feature")
	}
	list := feature.processList()
	if len(list) != 2 || list[0] != "rails" || list[1] != "webpack" {
		t.Fatalf("unexpected process list: %+v", list)
	}
	other := targets["other"]
	if other == nil || !other.all {
		t.Fatalf("expected wildcard target for other")
	}
}

func TestResolveProcessTargetsWildcardWins(t *testing.T) {
	targets, err := resolveProcessTargets([]string{
		"foo:rails",
		"foo:*",
		"foo:webpack",
	})
	if err != nil {
		t.Fatalf("resolveProcessTargets: %v", err)
	}
	target := targets["foo"]
	if target == nil {
		t.Fatalf("expected target foo")
	}
	if !target.all {
		t.Fatalf("expected wildcard all=true")
	}
	if target.processList() != nil {
		t.Fatalf("expected no specific process list when wildcard is set")
	}
}
