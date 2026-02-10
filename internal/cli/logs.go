package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"dev/internal/config"
	"dev/internal/worktree"
)

type logViewOptions struct {
	FollowFlag bool
	NoFollow   bool
	All        bool
	Lines      int
}

type logSource struct {
	Name string
	Path string
}

func newLogsCmd(opts *Options) *cobra.Command {
	view := &logViewOptions{}
	cmd := &cobra.Command{
		Use:   "logs [process|slug:process|project:slug:process]...",
		Short: "view process logs",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(opts, args, view)
		},
	}
	addLogFlags(cmd, view)
	return cmd
}

func newDaemonLogsCmd() *cobra.Command {
	view := &logViewOptions{}
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "view daemon logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir, err := config.ResolveStateDir(nil)
			if err != nil {
				return err
			}
			path := filepath.Join(stateDir, "logs", "daemon.log")
			return viewLogs([]logSource{{Name: "daemon", Path: path}}, effectiveFollow(view), view.All, view.Lines)
		},
	}
	addLogFlags(cmd, view)
	return cmd
}

func addLogFlags(cmd *cobra.Command, view *logViewOptions) {
	defaultFollow := term.IsTerminal(int(os.Stdin.Fd()))
	cmd.Flags().BoolVarP(&view.FollowFlag, "follow", "f", defaultFollow, "follow log output")
	cmd.Flags().BoolVarP(&view.NoFollow, "no-follow", "F", false, "disable following log output")
	cmd.Flags().BoolVar(&view.All, "all", false, "show entire log files")
	cmd.Flags().IntVarP(&view.Lines, "lines", "n", 20, "number of tail lines to show")
}

func effectiveFollow(view *logViewOptions) bool {
	if view == nil {
		return false
	}
	if view.NoFollow {
		return false
	}
	return view.FollowFlag
}

func runLogs(opts *Options, targets []string, view *logViewOptions) error {
	projectFromTarget, slug, processes, err := selectLogsTargets(targets)
	if err != nil {
		return err
	}
	if slug == "" {
		resolved, err := resolveSlug(opts, "")
		if err != nil {
			return err
		}
		slug = resolved
	}

	cwd := workingDir(opts)
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	path, err := worktree.ResolvePathFromSlugWithRegistry(slug, cwd, daemonCfg)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return err
	}
	project := cfg.Project.Name
	if project == "" {
		return errors.New("project.name is required")
	}
	if projectFromTarget != "" && projectFromTarget != project {
		return fmt.Errorf("target project %q does not match resolved project %q", projectFromTarget, project)
	}
	stateRoot, err := config.ResolveStateDir(nil)
	if err != nil {
		return err
	}
	worktreeDir := filepath.Join(stateRoot, "logs", project, slug)

	sources := []logSource{}
	if len(processes) > 0 {
		for _, process := range processes {
			sources = append(sources, logSource{
				Name: process,
				Path: filepath.Join(worktreeDir, process+".log"),
			})
		}
	} else {
		names := make([]string, 0, len(cfg.Processes))
		for name := range cfg.Processes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sources = append(sources, logSource{
				Name: name,
				Path: filepath.Join(worktreeDir, name+".log"),
			})
		}
	}
	return viewLogs(sources, effectiveFollow(view), view.All, view.Lines)
}

func selectLogsTargets(targets []string) (project string, slug string, processes []string, err error) {
	processes = make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		trimmed := strings.TrimSpace(target)
		if trimmed == "" {
			continue
		}
		id, err := worktree.ParseProcessIdentifier(trimmed)
		if err != nil {
			return "", "", nil, err
		}

		if id.Slug != "" {
			if slug == "" {
				slug = id.Slug
			} else if slug != id.Slug {
				return "", "", nil, fmt.Errorf("all targets must reference the same slug (got %q and %q)", slug, id.Slug)
			}
		}
		if id.Project != "" {
			if project == "" {
				project = id.Project
			} else if project != id.Project {
				return "", "", nil, fmt.Errorf("all targets must reference the same project (got %q and %q)", project, id.Project)
			}
		}

		if _, ok := seen[id.Process]; ok {
			continue
		}
		seen[id.Process] = struct{}{}
		processes = append(processes, id.Process)
	}
	return project, slug, processes, nil
}

func viewLogs(sources []logSource, follow bool, all bool, lines int) error {
	if lines <= 0 {
		lines = 20
	}
	for _, src := range sources {
		data, err := os.ReadFile(src.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Printf("[%s] (missing log file)\n", src.Name)
				continue
			}
			return err
		}
		printLogChunk(src.Name, data, len(sources) > 1, all, lines)
	}
	if !follow {
		return nil
	}
	offsets := map[string]int64{}
	for _, src := range sources {
		info, err := os.Stat(src.Path)
		if err != nil {
			offsets[src.Path] = 0
			continue
		}
		offsets[src.Path] = info.Size()
	}
	for {
		time.Sleep(300 * time.Millisecond)
		for _, src := range sources {
			info, err := os.Stat(src.Path)
			if err != nil {
				continue
			}
			start := offsets[src.Path]
			if info.Size() < start {
				start = 0
			}
			if info.Size() == start {
				continue
			}
			file, err := os.Open(src.Path)
			if err != nil {
				continue
			}
			_, _ = file.Seek(start, io.SeekStart)
			data, _ := io.ReadAll(file)
			_ = file.Close()
			offsets[src.Path] = info.Size()
			printLogBytes(src.Name, data, len(sources) > 1)
		}
	}
}

func printLogChunk(name string, data []byte, withPrefix bool, all bool, lines int) {
	if all {
		printLogBytes(name, data, withPrefix)
		return
	}
	split := bytes.Split(data, []byte("\n"))
	if len(split) > 0 && len(split[len(split)-1]) == 0 {
		split = split[:len(split)-1]
	}
	if len(split) > lines {
		split = split[len(split)-lines:]
	}
	for _, line := range split {
		if withPrefix {
			fmt.Printf("[%s] %s\n", name, string(line))
		} else {
			fmt.Println(string(line))
		}
	}
}

func printLogBytes(name string, data []byte, withPrefix bool) {
	if len(data) == 0 {
		return
	}
	if !withPrefix {
		_, _ = os.Stdout.Write(data)
		return
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if line == "" && i == len(lines)-1 {
			continue
		}
		fmt.Printf("[%s] %s\n", name, line)
	}
}
