package drift

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewHTTPPromClient_DefaultInsecure(t *testing.T) {
	c, err := NewHTTPPromClient("https://localhost:9090")
	if err != nil {
		t.Fatalf("NewHTTPPromClient: %v", err)
	}
	if c.baseURL != "https://localhost:9090" {
		t.Errorf("baseURL = %q, want https://localhost:9090", c.baseURL)
	}
	if c.client == nil {
		t.Fatal("expected non-nil http.Client")
	}
}

func TestNewHTTPPromClient_WithCACert(t *testing.T) {
	certPath := writeSelfSignedCert(t)

	c, err := NewHTTPPromClient("https://localhost:9090", WithCACert(certPath))
	if err != nil {
		t.Fatalf("NewHTTPPromClient with CA cert: %v", err)
	}
	if c.caCertPath != certPath {
		t.Errorf("caCertPath = %q, want %q", c.caCertPath, certPath)
	}
	if c.client == nil {
		t.Fatal("expected non-nil http.Client")
	}
}

func TestNewHTTPPromClient_WithCACert_InvalidPath(t *testing.T) {
	_, err := NewHTTPPromClient("https://localhost:9090", WithCACert("/nonexistent/ca.pem"))
	if err == nil {
		t.Fatal("expected error for nonexistent CA cert path")
	}
}

func TestNewHTTPPromClient_WithCACert_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badPath, []byte("not a PEM certificate"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewHTTPPromClient("https://localhost:9090", WithCACert(badPath))
	if err == nil {
		t.Fatal("expected error for invalid PEM content")
	}
}

func writeSelfSignedCert(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certPath, pemBytes, 0644); err != nil {
		t.Fatal(err)
	}
	return certPath
}
