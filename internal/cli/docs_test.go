package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestResolvePagerCommandOrder(t *testing.T) {
	t.Setenv("DEV_PAGER", "")
	t.Setenv("PAGER", "less -R")
	pager, err := resolvePagerCommand(func(name string) (string, error) {
		if name == "bat" {
			return "/usr/bin/bat", nil
		}
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatalf("resolvePagerCommand: %v", err)
	}
	if pager == nil || pager.name != "bat" {
		t.Fatalf("expected bat pager, got %#v", pager)
	}
}

func TestResolvePagerCommandUsesDevModePagerFirst(t *testing.T) {
	t.Setenv("DEV_PAGER", "less -R")
	t.Setenv("PAGER", "cat")
	pager, err := resolvePagerCommand(func(string) (string, error) {
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatalf("resolvePagerCommand: %v", err)
	}
	if pager == nil || pager.name != "less" || len(pager.args) != 1 || pager.args[0] != "-R" {
		t.Fatalf("unexpected pager: %#v", pager)
	}
}

func TestRunDocsFallsBackToStdout(t *testing.T) {
	t.Setenv("DEV_PAGER", "")
	t.Setenv("PAGER", "")
	var out bytes.Buffer
	var errOut bytes.Buffer
	err := runDocs("config", &out, &errOut, func(string) (string, error) {
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatalf("runDocs: %v", err)
	}
	if !strings.Contains(out.String(), "# dev Config") {
		t.Fatalf("expected config markdown in output")
	}
}

func TestRunDocsWithDevModePager(t *testing.T) {
	t.Setenv("DEV_PAGER", "cat")
	t.Setenv("PAGER", "")
	var out bytes.Buffer
	var errOut bytes.Buffer
	err := runDocs("usage", &out, &errOut, func(string) (string, error) {
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatalf("runDocs: %v", err)
	}
	if !strings.Contains(out.String(), "# dev Usage") {
		t.Fatalf("expected usage markdown in output")
	}
}

func TestCompleteDocsBasenames(t *testing.T) {
	all := completeDocsBasenames("")
	if len(all) == 0 {
		t.Fatalf("expected available docs")
	}
	foundConfig := false
	for _, name := range all {
		if name == "config" {
			foundConfig = true
			break
		}
	}
	if !foundConfig {
		t.Fatalf("expected config doc in completions: %#v", all)
	}

	filtered := completeDocsBasenames("us")
	if len(filtered) != 1 || filtered[0] != "usage" {
		t.Fatalf("unexpected filtered completions: %#v", filtered)
	}
}

func TestPrintAvailableDocs(t *testing.T) {
	var out bytes.Buffer
	if err := printAvailableDocs(&out); err != nil {
		t.Fatalf("printAvailableDocs: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Available docs:") {
		t.Fatalf("expected header in output, got %q", text)
	}
	if !strings.Contains(text, "config") {
		t.Fatalf("expected config in output, got %q", text)
	}
}
