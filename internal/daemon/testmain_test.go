package daemon

import (
	"fmt"
	"net"
	"os"
	"os/signal"
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
	os.Exit(m.Run())
}
