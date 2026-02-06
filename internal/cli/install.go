package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const installHomeEnv = "DEV_INSTALL_HOME"

func newInstallCmd() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "install system integrations (certs, DNS, proxy privileges)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(confirm)
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "skip confirmation prompt")
	cmd.Flags().BoolVarP(&confirm, "yes", "y", false, "skip confirmation prompt")
	_ = cmd.Flags().MarkHidden("confirm")
	return cmd
}

func runInstall(confirm bool) error {
	if runtime.GOOS == "windows" {
		return errors.New("install is not supported on windows")
	}

	if os.Geteuid() != 0 {
		if !confirm {
			fmt.Println("dev install needs sudo to:")
			fmt.Println("- write DNS resolver config")
			fmt.Println("- trust a local CA in the system store")
			fmt.Println("- enable binding to ports 80/443")
			fmt.Print("Continue with sudo? [y/N]: ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				return errors.New("install cancelled")
			}
		}
		return reexecWithSudo()
	}

	ensureInstallHome()

	if err := runCertInstall(); err != nil {
		return err
	}
	if err := runDNSInstall(); err != nil {
		return err
	}
	if err := installProxyPrivileges(); err != nil {
		return err
	}

	fmt.Println("install completed")
	return nil
}

func reexecWithSudo() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	home := os.Getenv("HOME")
	cmd := exec.Command("sudo", "-E", exe, "install", "--confirm")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), installHomeEnv+"="+home)
	return cmd.Run()
}

func ensureInstallHome() {
	if envHome := os.Getenv(installHomeEnv); envHome != "" {
		_ = os.Setenv("HOME", envHome)
		return
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil {
			_ = os.Setenv("HOME", u.HomeDir)
		}
	}
}

func installProxyPrivileges() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("setcap", "cap_net_bind_service=+ep", exe)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
