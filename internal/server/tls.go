package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// LoadOrCreateCert loads certFile + keyFile. When both are empty it uses a
// self-signed certificate kept in <dataDir>/tls/, making one on first run
// for hosts (plus localhost, 127.0.0.1 and ::1). Clients trust it by
// pointing KIRCH_CA_FILE at <dataDir>/tls/cert.pem.
func LoadOrCreateCert(dataDir, certFile, keyFile string, hosts []string) (tls.Certificate, string, error) {
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return tls.Certificate{}, "", errors.New("set both KIRCH_TLS_CERT and KIRCH_TLS_KEY, or neither")
		}
		c, err := tls.LoadX509KeyPair(certFile, keyFile)
		return c, certFile, err
	}
	dir := filepath.Join(dataDir, "tls")
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if c, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return c, certFile, nil
	}
	certPEM, keyPEM, err := SelfSigned(hosts)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	return c, certFile, err
}

// SelfSigned makes a P-256 certificate (valid 2 years) for hosts plus
// localhost / 127.0.0.1 / ::1, returned as PEM.
func SelfSigned(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Kirch dev server", Organization: []string{"Kirch"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(2 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range append([]string{"localhost", "127.0.0.1", "::1"}, hosts...) {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("make certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}
