package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/config"
)

const (
	certValidity = 3650 * 24 * time.Hour
)

var osChown = os.Chown

func newCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "manage local certificates",
	}

	cmd.AddCommand(newCertInstallCmd())
	cmd.AddCommand(newCertExportCmd())
	return cmd
}

func newCertInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "create and trust a local CA",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCertInstall()
		},
	}
}

func newCertExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "write the local CA certificate to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCertExport(cmd.OutOrStdout())
		},
	}
}

func runCertInstall() error {
	dir, err := certsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	caKeyPath := filepath.Join(dir, "ca-key.pem")
	caCertPath := filepath.Join(dir, "ca.pem")
	leafKeyPath := filepath.Join(dir, "localhost-key.pem")
	leafCertPath := filepath.Join(dir, "localhost.pem")

	caKey, caCert, err := ensureCA(caKeyPath, caCertPath)
	if err != nil {
		return err
	}

	if err := ensureLeaf(caKey, caCert, leafKeyPath, leafCertPath); err != nil {
		return err
	}

	if runtime.GOOS == "darwin" {
		if err := trustCAOnDarwin(caCertPath); err != nil {
			return err
		}
	} else if runtime.GOOS == "linux" {
		if err := trustCAOnLinux(caCertPath); err != nil {
			return err
		}
	}

	if err := chownCertMaterialToInvoker(dir, caKeyPath, caCertPath, leafKeyPath, leafCertPath); err != nil {
		return err
	}

	fmt.Printf("certs written to %s\n", dir)
	return nil
}

func chownCertMaterialToInvoker(dir string, paths ...string) error {
	uid, gid, ok, err := invokingUserOwnership()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	allPaths := make([]string, 0, len(paths)+1)
	allPaths = append(allPaths, paths...)
	allPaths = append(allPaths, dir)
	for _, path := range allPaths {
		if err := osChown(path, uid, gid); err != nil {
			return fmt.Errorf("set ownership on %s: %w", path, err)
		}
	}
	return nil
}

func invokingUserOwnership() (uid int, gid int, ok bool, err error) {
	if getEUID() != 0 {
		return 0, 0, false, nil
	}

	sudoUID := os.Getenv("SUDO_UID")
	sudoGID := os.Getenv("SUDO_GID")
	if sudoUID != "" && sudoGID != "" {
		uid, err := strconv.Atoi(sudoUID)
		if err != nil {
			return 0, 0, false, fmt.Errorf("parse SUDO_UID %q: %w", sudoUID, err)
		}
		gid, err := strconv.Atoi(sudoGID)
		if err != nil {
			return 0, 0, false, fmt.Errorf("parse SUDO_GID %q: %w", sudoGID, err)
		}
		return uid, gid, true, nil
	}

	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		return 0, 0, false, nil
	}

	u, err := user.Lookup(sudoUser)
	if err != nil {
		return 0, 0, false, fmt.Errorf("lookup SUDO_USER %q: %w", sudoUser, err)
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse uid for SUDO_USER %q: %w", sudoUser, err)
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse gid for SUDO_USER %q: %w", sudoUser, err)
	}
	return uid, gid, true, nil
}

func runCertExport(out io.Writer) error {
	dir, err := certsDir()
	if err != nil {
		return err
	}
	caCertPath := filepath.Join(dir, "ca.pem")
	data, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("read local CA certificate %s (run dev cert install): %w", caCertPath, err)
	}
	_, err = out.Write(data)
	return err
}

func certsDir() (string, error) {
	path, err := config.ExpandUserPath("~/.config/dev/certs")
	if err != nil {
		return "", err
	}
	return path, nil
}

func ensureCA(keyPath, certPath string) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	if _, err := os.Stat(keyPath); err == nil {
		key, cert, err := loadCA(keyPath, certPath)
		if err == nil {
			return key, cert, nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serial, err := randSerial()
	if err != nil {
		return nil, nil, err
	}

	cert := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "dev Local CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	if err := writeKey(keyPath, key); err != nil {
		return nil, nil, err
	}
	if err := writeCert(certPath, certDER); err != nil {
		return nil, nil, err
	}

	return key, cert, nil
}

func ensureLeaf(caKey *ecdsa.PrivateKey, caCert *x509.Certificate, keyPath, certPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := randSerial()
	if err != nil {
		return err
	}

	cert := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "dev localhost",
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(certValidity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost", "*.localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, cert, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}

	if err := writeKey(keyPath, key); err != nil {
		return err
	}
	return writeCert(certPath, certDER)
}

func loadCA(keyPath, certPath string) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, nil, errors.New("invalid CA key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, errors.New("invalid CA cert")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	bytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	block := &pem.Block{Type: "EC PRIVATE KEY", Bytes: bytes}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func writeCert(path string, certDER []byte) error {
	block := &pem.Block{Type: "CERTIFICATE", Bytes: certDER}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o644)
}

func randSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func trustCAOnDarwin(certPath string) error {
	cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", filepath.Join(os.Getenv("HOME"), "Library/Keychains/login.keychain-db"), certPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func trustCAOnLinux(certPath string) error {
	if os.Geteuid() != 0 {
		fmt.Println("installing CA on Linux requires root privileges.")
		fmt.Println("rerun with sudo, or manually copy the CA to /usr/local/share/ca-certificates and run update-ca-certificates.")
		return nil
	}

	target := filepath.Join("/usr/local/share/ca-certificates", "dev-ca.crt")
	data, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return err
	}

	if _, err := exec.LookPath("update-ca-certificates"); err == nil {
		cmd := exec.Command("update-ca-certificates")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if _, err := exec.LookPath("trust"); err == nil {
		cmd := exec.Command("trust", "anchor", target)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return errors.New("no trusted CA installer found (expected update-ca-certificates or trust)")
}
