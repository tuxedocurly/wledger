package tlsutil

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

	"github.com/tuxedocurly/wledger/internal/config"
)

// ConfigureTLS resolves server TLS configuration from runtime settings.
// Returns (nil, false, nil) when TLS is disabled.
func ConfigureTLS(cfg config.Runtime) (*tls.Config, bool, error) {
	if cfg.TLSCert != "" || cfg.TLSKey != "" {
		if cfg.TLSCert == "" || cfg.TLSKey == "" {
			return nil, false, errors.New("both WLEDGER_TLS_CERT and WLEDGER_TLS_KEY must be set when using explicit TLS certificates")
		}
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, false, fmt.Errorf("failed to load TLS certificate/key pair: %w", err)
		}
		return newServerTLSConfig(cert), true, nil
	}

	if !cfg.TLSSelfSigned {
		return nil, false, nil
	}

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = config.DirData
	}
	tlsDir := filepath.Join(dataDir, "tls")
	cert, err := loadOrCreateSelfSignedCert(tlsDir)
	if err != nil {
		return nil, false, err
	}
	return newServerTLSConfig(cert), true, nil
}

func newServerTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}

func loadOrCreateSelfSignedCert(tlsDir string) (tls.Certificate, error) {
	certPath := filepath.Join(tlsDir, "cert.pem")
	keyPath := filepath.Join(tlsDir, "key.pem")

	if cert, ok := tryLoadValidCert(certPath, keyPath); ok {
		return cert, nil
	}

	if err := os.MkdirAll(tlsDir, 0700); err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to create TLS directory: %w", err)
	}

	certPEM, keyPEM, err := generateSelfSignedECDSA()
	if err != nil {
		return tls.Certificate{}, err
	}

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to write TLS certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to write TLS private key: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to parse generated TLS key pair: %w", err)
	}
	return cert, nil
}

func tryLoadValidCert(certPath, keyPath string) (tls.Certificate, bool) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil || len(cert.Certificate) == 0 {
		return tls.Certificate{}, false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, false
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return tls.Certificate{}, false
	}
	return cert, true
}

func generateSelfSignedECDSA() ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate ECDSA P-256 key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate certificate serial: %w", err)
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "wledger-localhost",
			Organization: []string{"wledger"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           collectSANIPs(),
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create self-signed certificate: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal EC private key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM, nil
}

func collectSANIPs() []net.IP {
	seen := make(map[string]struct{})
	var ips []net.IP

	addIP := func(ip net.IP) {
		if ip == nil {
			return
		}
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		key := ip.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		ips = append(ips, ip)
	}

	addIP(net.ParseIP("127.0.0.1"))
	addIP(net.ParseIP("::1"))

	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
				continue
			}
			addIP(ip)
		}
	}

	return ips
}
