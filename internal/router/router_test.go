package router

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dev/internal/config"
)

func TestParseMatchers_Subdomains(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".dev.toml")
	body := `
[project]
name = "demo"

[gateway]
expose = { mailpit = { mode = "rewrite", debug_log = "tmp/gateway-http.log" } }

[process.mailpit]
singleton = true
command = "mailpit"
proxy = { subdomain = "mailpit" }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	matchers := ParseMatchers(cfg)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Kind != SubdomainExplicit || matchers[0].Subdomain != "mailpit" {
		t.Fatalf("unexpected matcher: %+v", matchers[0])
	}
}

func TestParseMatchers_NamedPort(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".dev.toml")
	body := `
[project]
name = "demo"

[process.rails]
command = "rails"

[process.rails.ports]
http = "random"
grpc = "random"

[[process.rails.proxy]]
path = "/"
port = "http"

[[process.rails.proxy]]
subdomain = "grpc"
path = "/"
port = "grpc"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	matchers := ParseMatchers(cfg)
	if len(matchers) != 2 {
		t.Fatalf("expected 2 matchers, got %d", len(matchers))
	}
	byPort := map[string]string{}
	for _, m := range matchers {
		byPort[m.Port] = m.Subdomain
	}
	if byPort["http"] != "" {
		t.Fatalf("expected http matcher to have base subdomain, got %q", byPort["http"])
	}
	if byPort["grpc"] != "grpc" {
		t.Fatalf("expected grpc matcher subdomain=grpc, got %q", byPort["grpc"])
	}
}

func TestSelectMatcher_SubdomainPriority(t *testing.T) {
	matchers := []Matcher{
		{Process: "rails", Subdomain: "*", Kind: SubdomainWildcard, Path: "/deep/path", Match: "prefix"},
		{Process: "node", Subdomain: "app", Kind: SubdomainExplicit, Path: "/", Match: "prefix"},
	}
	best, ok := SelectMatcher(matchers, "app", "/deep/path/child")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestSelectMatcher_PathLengthAndPriority(t *testing.T) {
	pathMatchers := []Matcher{
		{Process: "rails", Subdomain: "*", Kind: SubdomainWildcard, Path: "/", Match: "prefix"},
		{Process: "node", Subdomain: "*", Kind: SubdomainWildcard, Path: "/api/v1", Match: "prefix"},
	}
	best, ok := SelectMatcher(pathMatchers, "app", "/api/v1/users")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}

	priorityMatchers := []Matcher{
		{Process: "rails", Subdomain: "*", Kind: SubdomainWildcard, Path: "/", Match: "prefix", Priority: 5},
		{Process: "node", Subdomain: "*", Kind: SubdomainWildcard, Path: "/", Match: "prefix", Priority: 10},
	}
	best, ok = SelectMatcher(priorityMatchers, "app", "/")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestMatchPath(t *testing.T) {
	ok, _ := MatchPath("/blah/x/y", "/blah/x", "prefix")
	if !ok {
		t.Fatalf("expected prefix match")
	}
	ok, _ = MatchPath("/rawz", "/raw*", "prefix")
	if !ok {
		t.Fatalf("expected raw prefix match")
	}
	ok, _ = MatchPath("/exact", "/exact", "exact")
	if !ok {
		t.Fatalf("expected exact match")
	}
	ok, _ = MatchPath("/exact/child", "/exact", "exact")
	if ok {
		t.Fatalf("expected exact mismatch")
	}
	ok, _ = MatchPath("/packs/app.js", "/packs/", "prefix")
	if !ok {
		t.Fatalf("expected trailing-slash prefix match")
	}
}

func TestResolveWithinWorktree_ConstrainsCandidates(t *testing.T) {
	r := New(".localhost")
	r.UpsertWorktree(WorktreeInput{
		RuntimeKey: "wt-a",
		Slug:       "main",
		RepoPath:   "/tmp/a",
		Labels:     []string{"foocorp"},
		Matchers: []Matcher{
			{Process: "frontend", Kind: SubdomainExplicit, Subdomain: "app", Path: "/", Match: "prefix"},
		},
	})
	r.UpsertWorktree(WorktreeInput{
		RuntimeKey: "wt-b",
		Slug:       "other",
		RepoPath:   "/tmp/b",
		Labels:     []string{"some-other-worktree"},
		Matchers: []Matcher{
			{Process: "frontend", Kind: SubdomainExplicit, Subdomain: "app", Path: "/", Match: "prefix"},
		},
	})

	_, err := r.ResolveWithinWorktree("wt-a", "app.some-other-worktree.localhost", "/")
	if !errors.Is(err, ErrHostNotMapped) {
		t.Fatalf("expected host-not-mapped for constrained worktree, got %v", err)
	}

	res, err := r.ResolveWithinWorktree("wt-b", "app.some-other-worktree.localhost", "/")
	if err != nil {
		t.Fatalf("resolve within worktree: %v", err)
	}
	if res.Matcher == nil || res.Matcher.Process != "frontend" {
		t.Fatalf("unexpected resolve matcher: %+v", res.Matcher)
	}
}

func TestResolve_ReturnsResolutionOnNoMatcher(t *testing.T) {
	r := New(".localhost")
	r.UpsertWorktree(WorktreeInput{
		RuntimeKey:       "wt-a",
		Slug:             "feature",
		RepoPath:         "/tmp/a",
		Labels:           []string{"feature"},
		DefaultSubdomain: "app",
		Matchers: []Matcher{
			{Process: "app", Kind: SubdomainExplicit, Subdomain: "app", Path: "/", Match: "prefix"},
		},
	})

	res, err := r.Resolve("feature.localhost", "/")
	if !errors.Is(err, ErrNoProxyMatcherMatched) {
		t.Fatalf("expected no-matcher error, got %v", err)
	}
	if res.RuntimeKey != "wt-a" {
		t.Fatalf("expected runtimeKey wt-a, got %q", res.RuntimeKey)
	}
	if res.Subdomain != "" {
		t.Fatalf("expected empty subdomain, got %q", res.Subdomain)
	}
	if res.Matcher != nil {
		t.Fatalf("expected nil matcher, got %+v", res.Matcher)
	}
	if res.DefaultSubdomain != "app" {
		t.Fatalf("expected DefaultSubdomain=app, got %q", res.DefaultSubdomain)
	}

	res, err = r.Resolve("unknown.feature.localhost", "/")
	if !errors.Is(err, ErrNoProxyMatcherMatched) {
		t.Fatalf("expected no-matcher error, got %v", err)
	}
	if res.Subdomain != "unknown" {
		t.Fatalf("expected subdomain unknown, got %q", res.Subdomain)
	}
	if res.DefaultSubdomain != "app" {
		t.Fatalf("expected DefaultSubdomain=app even on no-match, got %q", res.DefaultSubdomain)
	}
}
