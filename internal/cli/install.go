package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const installHomeEnv = "DEV_INSTALL_HOME"

var (
	currentGOOS               = runtime.GOOS
	getEUID                   = os.Geteuid
	osExecutable              = os.Executable
	runCommand                = exec.Command
	runCertInstallFn          = runCertInstall
	runDNSInstallFn           = runDNSInstall
	installProxyPrivilegesFn  = installProxyPrivileges
	reexecWithSudoFn          = reexecWithSudo
	certComponentInstalledFn  = certComponentInstalled
	dnsComponentInstalledFn   = dnsComponentInstalled
	proxyComponentInstalledFn = proxyComponentInstalled
)

type installStatus struct {
	certsInstalled bool
	dnsInstalled   bool
	proxyInstalled bool
}

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
	if currentGOOS == "windows" {
		return errors.New("install is not supported on windows")
	}

	ensureInstallHome()

	status, err := detectInstallStatus()
	if err != nil {
		return err
	}

	if status.certsInstalled {
		fmt.Println("certs already installed")
	}
	if status.dnsInstalled {
		fmt.Println("dns resolver already installed")
	}
	if currentGOOS == "linux" && status.proxyInstalled {
		fmt.Println("proxy privileges already installed")
	}
	if status.allInstalled() {
		fmt.Println("all components already installed")
		return nil
	}

	if getEUID() != 0 {
		if !confirm {
			fmt.Println("dev install needs sudo to:")
			for _, action := range status.missingActions() {
				fmt.Printf("- %s\n", action)
			}
			fmt.Print("Continue with sudo? [y/N]: ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				return errors.New("install cancelled")
			}
		}
		return reexecWithSudoFn()
	}

	if !status.certsInstalled {
		if err := runCertInstallFn(); err != nil {
			return err
		}
	}
	if !status.dnsInstalled {
		if err := runDNSInstallFn(); err != nil {
			return err
		}
	}
	if currentGOOS == "linux" && !status.proxyInstalled {
		if err := installProxyPrivilegesFn(); err != nil {
			return err
		}
	}

	fmt.Println("install completed")
	return nil
}

func detectInstallStatus() (installStatus, error) {
	certsInstalled, err := certComponentInstalledFn()
	if err != nil {
		return installStatus{}, err
	}
	dnsInstalled, err := dnsComponentInstalledFn()
	if err != nil {
		return installStatus{}, err
	}

	status := installStatus{
		certsInstalled: certsInstalled,
		dnsInstalled:   dnsInstalled,
		proxyInstalled: true,
	}
	if currentGOOS == "linux" {
		proxyInstalled, err := proxyComponentInstalledFn()
		if err != nil {
			return installStatus{}, err
		}
		status.proxyInstalled = proxyInstalled
	}
	return status, nil
}

func (s installStatus) allInstalled() bool {
	if currentGOOS == "linux" {
		return s.certsInstalled && s.dnsInstalled && s.proxyInstalled
	}
	return s.certsInstalled && s.dnsInstalled
}

func (s installStatus) missingActions() []string {
	actions := make([]string, 0, 3)
	if !s.dnsInstalled {
		actions = append(actions, "write DNS resolver config")
	}
	if !s.certsInstalled {
		actions = append(actions, "trust a local CA in the system store")
	}
	if currentGOOS == "linux" && !s.proxyInstalled {
		actions = append(actions, "enable binding to ports 80/443")
	}
	return actions
}

func reexecWithSudo() error {
	exe, err := osExecutable()
	if err != nil {
		return err
	}

	home := os.Getenv("HOME")
	cmd := runCommand("sudo", "-E", exe, "install", "--confirm")
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
	if currentGOOS != "linux" {
		return nil
	}
	exe, err := osExecutable()
	if err != nil {
		return err
	}
	cmd := runCommand("setcap", "cap_net_bind_service=+ep", exe)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func certComponentInstalled() (bool, error) {
	dir, err := certsDir()
	if err != nil {
		return false, err
	}
	required := []string{
		filepath.Join(dir, "ca-key.pem"),
		filepath.Join(dir, "ca.pem"),
		filepath.Join(dir, "localhost-key.pem"),
		filepath.Join(dir, "localhost.pem"),
	}
	for _, path := range required {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

func dnsComponentInstalled() (bool, error) {
	var path, want string
	switch currentGOOS {
	case "darwin":
		path = resolverPath()
		want = resolverContent()
	case "linux":
		path = linuxResolverPath()
		want = linuxResolverContent()
	default:
		return false, errors.New("dns install is only implemented for macOS and Linux")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(data)) == strings.TrimSpace(want), nil
}

func proxyComponentInstalled() (bool, error) {
	if currentGOOS != "linux" {
		return true, nil
	}

	exe, err := osExecutable()
	if err != nil {
		return false, err
	}

	out, err := runCommand("getcap", exe).Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}

	return strings.Contains(string(out), "cap_net_bind_service"), nil
}
