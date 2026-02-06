package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mattn/go-shellwords"
	"github.com/spf13/cobra"

	docstore "dev/docs"
)

type pagerCommand struct {
	name string
	args []string
}

func newDocsCmd(opts *Options) *cobra.Command {
	_ = opts
	available := docstore.Available()
	long := "show embedded docs markdown"
	if len(available) > 0 {
		long = fmt.Sprintf("show embedded docs markdown\n\nAvailable docs: %s", strings.Join(available, ", "))
	}
	cmd := &cobra.Command{
		Use:   "docs <basename>",
		Short: "show embedded docs markdown",
		Long:  long,
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeDocsBasenames(toComplete), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return printAvailableDocs(cmd.OutOrStdout())
			}
			return runDocs(args[0], cmd.OutOrStdout(), cmd.ErrOrStderr(), exec.LookPath)
		},
	}
	return cmd
}

func printAvailableDocs(out io.Writer) error {
	available := docstore.Available()
	if len(available) == 0 {
		_, err := fmt.Fprintln(out, "No embedded docs found.")
		return err
	}
	if _, err := fmt.Fprintln(out, "Available docs:"); err != nil {
		return err
	}
	for _, name := range available {
		if _, err := fmt.Fprintf(out, "  - %s\n", name); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "\nUse: dev docs <basename>")
	return err
}

func completeDocsBasenames(toComplete string) []string {
	basenames := docstore.Available()
	if toComplete == "" {
		return basenames
	}
	out := make([]string, 0, len(basenames))
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	for _, name := range basenames {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			out = append(out, name)
		}
	}
	return out
}

func runDocs(basename string, out, errOut io.Writer, lookPath func(string) (string, error)) error {
	content, _, err := docstore.Lookup(basename)
	if err != nil {
		return err
	}
	pager, err := resolvePagerCommand(lookPath)
	if err != nil {
		return err
	}
	if pager == nil {
		_, err := out.Write(content)
		return err
	}
	return runMarkdownThroughPager(content, pager, out, errOut)
}

func resolvePagerCommand(lookPath func(string) (string, error)) (*pagerCommand, error) {
	if cmd, err := parsePagerEnv("DEV_PAGER"); err != nil {
		return nil, err
	} else if cmd != nil {
		return cmd, nil
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("bat"); err == nil {
		return &pagerCommand{name: "bat", args: []string{"-l", "markdown", "-p"}}, nil
	}
	if cmd, err := parsePagerEnv("PAGER"); err != nil {
		return nil, err
	} else if cmd != nil {
		return cmd, nil
	}
	return nil, nil
}

func parsePagerEnv(name string) (*pagerCommand, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	args, err := shellwords.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, fmt.Errorf("%s is set but empty", name)
	}
	return &pagerCommand{name: args[0], args: args[1:]}, nil
}

func runMarkdownThroughPager(content []byte, pager *pagerCommand, out, errOut io.Writer) error {
	if pager == nil {
		return errors.New("missing pager command")
	}
	cmd := exec.Command(pager.name, pager.args...)
	cmd.Stdin = bytes.NewReader(content)
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run pager %q: %w", pager.name, err)
	}
	return nil
}
