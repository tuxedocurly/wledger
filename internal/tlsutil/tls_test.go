package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/config"
)

func TestConfigureTLS_Disabled(t *testing.T) {
	tlsCfg, enabled, err := ConfigureTLS(config.Runtime{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enabled || tlsCfg != nil {
		t.Fatalf("expected disabled TLS, got enabled=%v cfg=%v", enabled, tlsCfg)
	}
}

func TestConfigureTLS_RequiresBothExplicitFiles(t *testing.T) {
	if _, _, err := ConfigureTLS(config.Runtime{TLSCert: "cert.pem"}); err == nil {
		t.Fatal("expected error when only TLSCert is provided")
	}
	if _, _, err := ConfigureTLS(config.Runtime{TLSKey: "key.pem"}); err == nil {
		t.Fatal("expected error when only TLSKey is provided")
	}
}

func TestConfigureTLS_ExplicitCertAndKey(t *testing.T) {
	certPEM, keyPEM, err := generateSelfSignedECDSA()
	if err != nil {
		t.Fatalf("generateSelfSignedECDSA: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "explicit-cert.pem")
	keyPath := filepath.Join(dir, "explicit-key.pem")
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	tlsCfg, enabled, err := ConfigureTLS(config.Runtime{
		TLSCert: certPath,
		TLSKey:  keyPath,
	})
	if err != nil {
		t.Fatalf("ConfigureTLS explicit failed: %v", err)
	}
	if !enabled || tlsCfg == nil || len(tlsCfg.Certificates) != 1 {
		t.Fatalf("expected 1 loaded certificate, enabled=%v", enabled)
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion TLS 1.2, got %v", tlsCfg.MinVersion)
	}
}

func TestConfigureTLS_SelfSignedGeneratesAndPersistsECDSACert(t *testing.T) {
	dataDir := t.TempDir()
	cfg := config.Runtime{
		DataDir:       dataDir,
		TLSSelfSigned: true,
	}

	tlsCfg, enabled, err := ConfigureTLS(cfg)
	if err != nil {
		t.Fatalf("ConfigureTLS self-signed failed: %v", err)
	}
	if !enabled || tlsCfg == nil || len(tlsCfg.Certificates) != 1 {
		t.Fatalf("expected enabled self-signed TLS, got enabled=%v", enabled)
	}

	certPath := filepath.Join(dataDir, "tls", "cert.pem")
	keyPath := filepath.Join(dataDir, "tls", "key.pem")
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected persisted cert at %s: %v", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected persisted key at %s: %v", keyPath, err)
	}

	leaf, err := x509.ParseCertificate(tlsCfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate failed: %v", err)
	}

	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("expected ECDSA public key, got %T", leaf.PublicKey)
	}
	if pub.Curve != elliptic.P256() {
		t.Fatalf("expected ECDSA P-256 curve, got %v", pub.Curve)
	}

	hasLocalhost := false
	for _, dns := range leaf.DNSNames {
		if dns == "localhost" {
			hasLocalhost = true
		}
	}
	if !hasLocalhost {
		t.Fatalf("expected DNS SAN 'localhost', got %v", leaf.DNSNames)
	}

	ipSet := make(map[string]bool)
	for _, ip := range leaf.IPAddresses {
		ipSet[ip.String()] = true
	}
	for _, expected := range collectSANIPs() {
		if !ipSet[expected.String()] {
			t.Fatalf("missing expected SAN IP %s in cert SANs %v", expected.String(), leaf.IPAddresses)
		}
	}
	if !ipSet[net.ParseIP("127.0.0.1").String()] || !ipSet[net.ParseIP("::1").String()] {
		t.Fatalf("expected 127.0.0.1 and ::1 in IP SANs, got %v", leaf.IPAddresses)
	}

	// Second call must reuse the persisted cert rather than regenerating.
	tlsCfg2, enabled2, err := ConfigureTLS(cfg)
	if err != nil || !enabled2 {
		t.Fatalf("second ConfigureTLS failed: enabled=%v err=%v", enabled2, err)
	}
	leaf2, err := x509.ParseCertificate(tlsCfg2.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate2 failed: %v", err)
	}
	if leaf.SerialNumber.Cmp(leaf2.SerialNumber) != 0 {
		t.Fatalf("expected persisted certificate reuse, serial %v != %v", leaf.SerialNumber, leaf2.SerialNumber)
	}
}
