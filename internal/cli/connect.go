package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"dev-mode/internal/daemon"
)

func newAttachCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <process|slug:process|project/slug:process>",
		Short: "attach to a process PTY",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return errors.New("attach is not supported on windows")
			}
			slug, process, _, err := parseAttachTarget(args[0])
			if err != nil {
				return err
			}
			return runAttach(opts, process, slug)
		},
	}
	return cmd
}

func runAttach(opts *Options, process, slug string) error {
	if process == "" {
		return errors.New("process is required")
	}
	if slug == "" {
		resolved, err := resolveSlug("")
		if err != nil {
			return err
		}
		slug = resolved
	}
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}
	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	query := url.Values{}
	query.Set("slug", slug)
	query.Set("process", process)
	requestLine := fmt.Sprintf("GET /processes/connect?%s HTTP/1.1\r\nHost: unix\r\n\r\n", query.Encode())
	if _, err := conn.Write([]byte(requestLine)); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.Contains(statusLine, "200") {
		return fmt.Errorf("attach failed: %s", strings.TrimSpace(statusLine))
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			break
		}
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, reader)
		errCh <- err
	}()

	return <-errCh
}

func parseAttachTarget(arg string) (slug string, process string, project string, err error) {
	if arg == "" {
		return "", "", "", errors.New("target is required")
	}
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) == 1 {
		return "", parts[0], "", nil
	}
	if parts[1] == "" {
		return "", "", "", errors.New("process is required after ':'")
	}
	left := parts[0]
	if left == "" {
		return "", "", "", errors.New("slug is required before ':'")
	}
	if strings.Contains(left, "/") {
		projectSlug := strings.SplitN(left, "/", 2)
		if len(projectSlug) != 2 || projectSlug[0] == "" || projectSlug[1] == "" {
			return "", "", "", errors.New("expected project/slug before ':'")
		}
		return projectSlug[1], parts[1], projectSlug[0], nil
	}
	return left, parts[1], "", nil
}
