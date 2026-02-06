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
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"dev/internal/daemon"
	"dev/internal/worktree"
)

const (
	detachPrefixByte  = 0x01 // Ctrl-A
	detachTriggerByte = 0x04 // Ctrl-D
	detachTimeout     = time.Second
)

var errDetachRequested = errors.New("detach requested")

func newAttachCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <process|slug:process|project:slug:process>",
		Short: "attach to a process PTY",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return errors.New("attach is not supported on windows")
			}
			id, err := worktree.ParseProcessIdentifier(args[0])
			if err != nil {
				return err
			}
			return runAttach(opts, id.Process, id.Slug)
		},
	}
	return cmd
}

func runAttach(opts *Options, process, slug string) error {
	if process == "" {
		return errors.New("process is required")
	}
	if slug == "" {
		resolved, err := resolveSlug(opts, "")
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
		err := forwardAttachInput(conn, os.Stdin, detachTimeout)
		if errors.Is(err, errDetachRequested) {
			errCh <- nil
			_ = conn.Close()
			return
		}
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, reader)
		errCh <- err
	}()

	return <-errCh
}

func forwardAttachInput(dst io.Writer, src io.Reader, timeout time.Duration) error {
	type readEvent struct {
		b   byte
		err error
	}
	eventCh := make(chan readEvent, 1)

	go func() {
		buf := make([]byte, 1)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				eventCh <- readEvent{b: buf[0]}
			}
			if err != nil {
				eventCh <- readEvent{err: err}
				return
			}
		}
	}()

	var (
		pendingPrefix bool
		timer         *time.Timer
		timerCh       <-chan time.Time
	)

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerCh = nil
	}

	flushPrefix := func() error {
		if !pendingPrefix {
			return nil
		}
		pendingPrefix = false
		_, err := dst.Write([]byte{detachPrefixByte})
		return err
	}

	startPendingPrefix := func() {
		pendingPrefix = true
		timer = time.NewTimer(timeout)
		timerCh = timer.C
	}

	for {
		select {
		case event := <-eventCh:
			if event.err != nil {
				stopTimer()
				if err := flushPrefix(); err != nil {
					return err
				}
				if errors.Is(event.err, io.EOF) {
					return nil
				}
				return event.err
			}
			b := event.b
			if !pendingPrefix {
				if b == detachPrefixByte {
					startPendingPrefix()
					continue
				}
				if _, err := dst.Write([]byte{b}); err != nil {
					stopTimer()
					return err
				}
				continue
			}

			stopTimer()
			if b == detachTriggerByte {
				return errDetachRequested
			}
			if err := flushPrefix(); err != nil {
				return err
			}
			if b == detachPrefixByte {
				startPendingPrefix()
				continue
			}
			if _, err := dst.Write([]byte{b}); err != nil {
				return err
			}

		case <-timerCh:
			stopTimer()
			if err := flushPrefix(); err != nil {
				return err
			}
		}
	}
}
