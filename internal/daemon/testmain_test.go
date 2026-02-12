package daemon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	// When re-executed as a subprocess with DEV_TEST_TCP_LISTEN=1, act as a
	// simple TCP listener on $PORT. This replaces "python3 -m http.server"
	// in integration tests so they don't depend on Python.
	if os.Getenv("DEV_TEST_TCP_LISTEN") == "1" {
		port := os.Getenv("PORT")
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listen: %v\n", err)
			os.Exit(1)
		}
		defer ln.Close()
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		os.Exit(0)
	}

	// Signal helper modes — used by process_signal_test.go to test shutdown
	// escalation with real Go processes (avoids POSIX SIG_IGN inheritance
	// issues with shell background jobs).
	switch os.Getenv("DEV_TEST_SIGNAL_HELPER") {
	case "child":
		// Block forever with default SIGINT handling (dies on SIGINT).
		// Notify on SIGTERM keeps the runtime alive (avoids deadlock panic).
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		<-ch
		os.Exit(0)
	case "child-ignore-int":
		// Block forever, ignoring SIGINT (only SIGKILL can stop this).
		signal.Ignore(syscall.SIGINT)
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		<-ch
		os.Exit(0)
	case "graceful":
		signalHelperParent(true, false)
		return
	case "ignore":
		signalHelperParent(false, false)
		return
	case "ignore-all":
		signalHelperParent(false, true)
		return
	}

	os.Exit(m.Run())
}

// signalHelperParent spawns a child process and manages signal handling.
// When graceful is true, it catches SIGINT and kills the child before exiting.
// Otherwise it ignores SIGINT. The child's SIGINT handling depends on
// childIgnoreINT.
func signalHelperParent(graceful, childIgnoreINT bool) {
	childMode := "child"
	if childIgnoreINT {
		childMode = "child-ignore-int"
	}

	// Start child BEFORE changing our own signal disposition so the child
	// inherits default SIGINT handling (not SIG_IGN).
	child := exec.Command(os.Args[0], "-test.run=^$")
	// Build a clean env: filter out our own DEV_TEST_SIGNAL_HELPER so the
	// child gets the right mode (os.Getenv returns the first match).
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "DEV_TEST_SIGNAL_HELPER=") {
			child.Env = append(child.Env, e)
		}
	}
	child.Env = append(child.Env, "DEV_TEST_SIGNAL_HELPER="+childMode)
	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start child: %v\n", err)
		os.Exit(1)
	}

	pidFile := os.Getenv("CHILD_PID_FILE")
	if pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", child.Process.Pid)), 0644)
	}

	if graceful {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT)
		<-ch
		_ = child.Process.Kill()
		_ = child.Wait()
		os.Exit(0)
	}

	signal.Ignore(syscall.SIGINT)
	_ = child.Wait()
}
