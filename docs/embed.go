package docs

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed *.md
var content embed.FS

var (
	indexOnce sync.Once
	indexErr  error
	indexByID map[string]string
)

func Lookup(basename string) ([]byte, string, error) {
	if err := buildIndex(); err != nil {
		return nil, "", err
	}
	id := normalizeBasename(basename)
	if id == "" {
		return nil, "", fmt.Errorf("doc basename is required")
	}
	filename, ok := indexByID[id]
	if !ok {
		return nil, "", fmt.Errorf("unknown doc %q (available: %s)", basename, strings.Join(Available(), ", "))
	}
	data, err := content.ReadFile(filename)
	if err != nil {
		return nil, "", err
	}
	return data, filename, nil
}

func Available() []string {
	if err := buildIndex(); err != nil {
		return nil
	}
	out := make([]string, 0, len(indexByID))
	for id := range indexByID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func buildIndex() error {
	indexOnce.Do(func() {
		entries, err := fs.ReadDir(content, ".")
		if err != nil {
			indexErr = err
			return
		}
		indexByID = map[string]string{}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if filepath.Ext(name) != ".md" {
				continue
			}
			id := normalizeBasename(strings.TrimSuffix(name, ".md"))
			if id == "" {
				continue
			}
			indexByID[id] = name
		}
	})
	return indexErr
}

func normalizeBasename(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimSuffix(trimmed, ".md")
	return strings.ToLower(trimmed)
}
