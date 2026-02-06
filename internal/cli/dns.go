package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

const (
	dnsResolverName = "localhost"
	dnsResolverIP   = "127.0.0.1"
	dnsResolverPort = 15353
)

func newDNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "manage local DNS resolver",
	}

	cmd.AddCommand(newDNSInstallCmd())
	cmd.AddCommand(newDNSUninstallCmd())
	return cmd
}

func newDNSInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "install local DNS resolver",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDNSInstall()
		},
	}
}

func newDNSUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "remove local DNS resolver",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDNSUninstall()
		},
	}
}

func runDNSInstall() error {
	switch runtime.GOOS {
	case "darwin":
		path := resolverPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}

		content := resolverContent()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			if os.IsPermission(err) {
				return fmt.Errorf("write resolver: %w (try re-running with sudo)", err)
			}
			return fmt.Errorf("write resolver: %w", err)
		}

		fmt.Printf("installed resolver at %s\n", path)
		return nil
	case "linux":
		return installLinuxResolver()
	default:
		return errors.New("dns install is only implemented for macOS and Linux")
	}
}

func runDNSUninstall() error {
	switch runtime.GOOS {
	case "darwin":
		path := resolverPath()
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		fmt.Printf("removed resolver at %s\n", path)
		return nil
	case "linux":
		return uninstallLinuxResolver()
	default:
		return errors.New("dns uninstall is only implemented for macOS and Linux")
	}
}

func resolverPath() string {
	return filepath.Join("/etc/resolver", dnsResolverName)
}

func resolverContent() string {
	return fmt.Sprintf("nameserver %s\nport %d\n", dnsResolverIP, dnsResolverPort)
}

func linuxResolverPath() string {
	return filepath.Join("/etc/systemd/resolved.conf.d", "dev-localhost.conf")
}

func linuxResolverContent() string {
	return fmt.Sprintf("[Resolve]\nDNS=%s:%d\nDomains=~%s\n", dnsResolverIP, dnsResolverPort, dnsResolverName)
}

func installLinuxResolver() error {
	path := linuxResolverPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(linuxResolverContent()), 0o644); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("write resolver: %w (try re-running with sudo)", err)
		}
		return fmt.Errorf("write resolver: %w", err)
	}
	fmt.Printf("installed resolver at %s\n", path)
	fmt.Println("restart systemd-resolved to apply: sudo systemctl restart systemd-resolved")
	return nil
}

func uninstallLinuxResolver() error {
	path := linuxResolverPath()
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	fmt.Printf("removed resolver at %s\n", path)
	fmt.Println("restart systemd-resolved to apply: sudo systemctl restart systemd-resolved")
	return nil
}
