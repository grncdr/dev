package cli

import (
	"errors"
	"testing"
)

func TestRunInstall_AllComponentsInstalledSkipsWork(t *testing.T) {
	restore := stubInstallHooks()
	defer restore()

	currentGOOS = "linux"
	getEUID = func() int { return 1000 }
	certComponentInstalledFn = func() (bool, error) { return true, nil }
	dnsComponentInstalledFn = func() (bool, error) { return true, nil }
	proxyComponentInstalledFn = func() (bool, error) { return true, nil }

	var sudoCalls int
	reexecWithSudoFn = func() error {
		sudoCalls++
		return nil
	}

	var certCalls, dnsCalls, proxyCalls int
	runCertInstallFn = func() error { certCalls++; return nil }
	runDNSInstallFn = func() error { dnsCalls++; return nil }
	installProxyPrivilegesFn = func() error { proxyCalls++; return nil }

	if err := runInstall(true); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if sudoCalls != 0 {
		t.Fatalf("expected no sudo calls, got %d", sudoCalls)
	}
	if certCalls != 0 || dnsCalls != 0 || proxyCalls != 0 {
		t.Fatalf("expected no install calls, got cert=%d dns=%d proxy=%d", certCalls, dnsCalls, proxyCalls)
	}
}

func TestRunInstall_NonRootWithMissingComponentReexecs(t *testing.T) {
	restore := stubInstallHooks()
	defer restore()

	currentGOOS = "linux"
	getEUID = func() int { return 1000 }
	certComponentInstalledFn = func() (bool, error) { return false, nil }
	dnsComponentInstalledFn = func() (bool, error) { return true, nil }
	proxyComponentInstalledFn = func() (bool, error) { return true, nil }

	var sudoCalls int
	reexecWithSudoFn = func() error {
		sudoCalls++
		return nil
	}

	if err := runInstall(true); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if sudoCalls != 1 {
		t.Fatalf("expected one sudo call, got %d", sudoCalls)
	}
}

func TestRunInstall_RootInstallsOnlyMissingComponents(t *testing.T) {
	restore := stubInstallHooks()
	defer restore()

	currentGOOS = "linux"
	getEUID = func() int { return 0 }
	certComponentInstalledFn = func() (bool, error) { return false, nil }
	dnsComponentInstalledFn = func() (bool, error) { return true, nil }
	proxyComponentInstalledFn = func() (bool, error) { return false, nil }

	var certCalls, dnsCalls, proxyCalls int
	runCertInstallFn = func() error { certCalls++; return nil }
	runDNSInstallFn = func() error { dnsCalls++; return nil }
	installProxyPrivilegesFn = func() error { proxyCalls++; return nil }

	if err := runInstall(true); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if certCalls != 1 || dnsCalls != 0 || proxyCalls != 1 {
		t.Fatalf("expected cert=1 dns=0 proxy=1, got cert=%d dns=%d proxy=%d", certCalls, dnsCalls, proxyCalls)
	}
}

func TestRunInstall_CheckFailureStopsInstall(t *testing.T) {
	restore := stubInstallHooks()
	defer restore()

	currentGOOS = "linux"
	getEUID = func() int { return 0 }
	wantErr := errors.New("probe failed")
	certComponentInstalledFn = func() (bool, error) { return false, wantErr }
	dnsComponentInstalledFn = func() (bool, error) { return false, nil }
	proxyComponentInstalledFn = func() (bool, error) { return false, nil }

	err := runInstall(true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func stubInstallHooks() func() {
	prevGOOS := currentGOOS
	prevGetEUID := getEUID
	prevRunCertInstall := runCertInstallFn
	prevRunDNSInstall := runDNSInstallFn
	prevInstallProxy := installProxyPrivilegesFn
	prevReexec := reexecWithSudoFn
	prevCertInstalled := certComponentInstalledFn
	prevDNSInstalled := dnsComponentInstalledFn
	prevProxyInstalled := proxyComponentInstalledFn

	return func() {
		currentGOOS = prevGOOS
		getEUID = prevGetEUID
		runCertInstallFn = prevRunCertInstall
		runDNSInstallFn = prevRunDNSInstall
		installProxyPrivilegesFn = prevInstallProxy
		reexecWithSudoFn = prevReexec
		certComponentInstalledFn = prevCertInstalled
		dnsComponentInstalledFn = prevDNSInstalled
		proxyComponentInstalledFn = prevProxyInstalled
	}
}
