package worktree

import (
	"fmt"
	"os/exec"
	"strings"
)

// ResolveBranch returns the branch name used in the managed process environment.
func ResolveBranch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Repositories without commits do not have a resolvable HEAD.
		if strings.Contains(string(output), "ambiguous argument 'HEAD'") {
			return "HEAD", nil
		}
		return "", fmt.Errorf("resolve worktree branch: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
