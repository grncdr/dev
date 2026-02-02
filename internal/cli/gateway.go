package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"dev-mode/internal/config"
	"dev-mode/internal/gateway"
)

func newGatewayCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "gateway",
		Short: "run the gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGateway(opts)
		},
	}
}

func runGateway(opts *Options) error {
	userCfg, _, err := config.LoadUserConfig(opts.ResolvedPaths.UserConfig)
	if err != nil {
		return err
	}
	dataDir, err := config.ResolveGatewayDataDir(userCfg)
	if err != nil {
		return err
	}

	listen := userCfg.Gateway.Listen
	if listen == "" {
		listen = ":443"
	}

	srv, err := gateway.NewServer(listen, dataDir, userCfg.Gateway.Auth)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	fmt.Printf("gateway listening on %s (data dir: %s)\n", srv.Addr(), dataDir)
	if userCfg.Gateway.Auth.Enabled {
		fmt.Println("gateway basic auth enabled")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
		fmt.Printf("gateway stopped (%s)\n", sig.String())
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
