package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnsureSelfSignedCert generates a P-256 self-signed cert+key at certPath/
// keyPath when either file is missing. Existing files are left untouched so
// operators who swap in a real cert don't have it overwritten on restart.
//
// The cert is valid for 10 years with SANs for every local IP plus
// localhost, so browsers on the LAN don't trip SAN mismatch warnings.
// Returns the SHA-256 fingerprint of the DER certificate so the caller can
// log it for out-of-band verification.
func EnsureSelfSignedCert(certPath, keyPath string, logger *slog.Logger) (fingerprint string, err error) {
	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)
	if certExists && keyExists {
		fp, ferr := fingerprintCert(certPath)
		if ferr != nil {
			logger.Warn("tls: existing cert unreadable, leaving in place", "err", ferr)
			return "", nil
		}
		return fp, nil
	}
	if certExists != keyExists {
		return "", fmt.Errorf("tls: only one of cert (%s) / key (%s) exists; refusing to overwrite", certPath, keyPath)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("tls: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", fmt.Errorf("tls: generate serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "ventd"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:    dnsSANs(),
		IPAddresses: ipSANs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return "", fmt.Errorf("tls: create certificate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return "", fmt.Errorf("tls: mkdir %s: %w", filepath.Dir(certPath), err)
	}
	if err := writeFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return "", fmt.Errorf("tls: write cert: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("tls: marshal key: %w", err)
	}
	if err := writeFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return "", fmt.Errorf("tls: write key: %w", err)
	}

	fp := fingerprintDER(der)
	logger.Info("tls: generated self-signed certificate",
		"cert", certPath, "key", keyPath,
		"sha256", fp, "expires", tmpl.NotAfter.Format(time.RFC3339))
	return fp, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fingerprintCert(certPath string) (string, error) {
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("tls: read %s: %w", certPath, err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("no CERTIFICATE block in %s", certPath)
	}
	return fingerprintDER(block.Bytes), nil
}

func fingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	hexStr := hex.EncodeToString(sum[:])
	var b strings.Builder
	for i := 0; i < len(hexStr); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hexStr[i : i+2])
	}
	return b.String()
}

// dnsSANs returns the DNS names embedded in the cert. "localhost" covers
// the loopback browser case; the machine hostname covers a local mDNS
// lookup like "ventd.local".
func dnsSANs() []string {
	names := []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, h)
		if !strings.Contains(h, ".") {
			names = append(names, h+".local")
		}
	}
	return names
}

// ipSANs enumerates every non-link-local unicast IP on the host so a
// browser hitting https://<LAN-ip>:9999 validates the cert.
func ipSANs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() || n.IP.IsMulticast() {
			continue
		}
		ips = append(ips, n.IP)
	}
	return ips
}

// writeFile writes atomically via tmp+rename so a crashed write never
// leaves a half-formed key readable by the daemon on next boot.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("tls: open %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("tls: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("tls: sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("tls: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("tls: rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
