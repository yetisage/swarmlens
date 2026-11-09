package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pki is a throwaway certificate authority for the TLS tests.
type pki struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	caPEM []byte
}

func newPKI(t *testing.T, name string) *pki {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &pki{cert: cert, key: key, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue makes a certificate for 127.0.0.1 signed by the authority.
func (p *pki) issue(t *testing.T, cn string, usage x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.cert, &key.PublicKey, p.key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
}

// serveTLS runs the recorded daemon over TLS with a certificate from serverCA,
// asking for client certificates from clientCA if that is not nil.
func serveTLS(t *testing.T, serverCA, clientCA *pki) *httptest.Server {
	t.Helper()
	certPEM, keyPEM := serverCA.issue(t, "daemon", x509.ExtKeyUsageServerAuth)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(recordedHandler(t))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	if clientCA != nil {
		pool := x509.NewCertPool()
		pool.AddCert(clientCA.cert)
		srv.TLS.ClientCAs, srv.TLS.ClientAuth = pool, tls.RequireAndVerifyClientCert
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// certDir lays out what DOCKER_CERT_PATH points at.
func certDir(t *testing.T, ca *pki, client *pki) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ca.pem", ca.caPEM)
	if client != nil {
		cert, key := client.issue(t, "operator", x509.ExtKeyUsageClientAuth)
		write("cert.pem", cert)
		write("key.pem", key)
	}
	return dir
}

func tlsEnv(t *testing.T, srv *httptest.Server, certPath string) {
	t.Helper()
	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	t.Setenv("DOCKER_CERT_PATH", certPath)
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("DOCKER_CONTEXT", "")
}

func summarize(t *testing.T) (*Summary, error) {
	t.Helper()
	a, err := Connect(ConnectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = a.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.Summary(ctx)
}

func TestMutualTLSFromTheEnvironment(t *testing.T) {
	ca := newPKI(t, "test CA")
	srv := serveTLS(t, ca, ca)
	tlsEnv(t, srv, certDir(t, ca, ca))
	sum, err := summarize(t)
	if err != nil || sum.NodeName != "vm" {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
}

// A daemon that does not ask for a client certificate is a normal setup: the
// CA alone is enough.
func TestTLSWithoutAClientCertificate(t *testing.T) {
	ca := newPKI(t, "test CA")
	srv := serveTLS(t, ca, nil)
	tlsEnv(t, srv, certDir(t, ca, nil))
	if sum, err := summarize(t); err != nil || sum.NodeName != "vm" {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
}

// Verification must not be quietly dropped: a server whose certificate the
// CA did not sign is refused.
func TestTLSRefusesAnUntrustedServer(t *testing.T) {
	trusted, impostor := newPKI(t, "trusted CA"), newPKI(t, "impostor CA")
	srv := serveTLS(t, impostor, nil)
	tlsEnv(t, srv, certDir(t, trusted, nil))
	_, err := summarize(t)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("err = %v, want a certificate error", err)
	}
}

func TestTLSRefusesAClientWithoutTheRightCertificate(t *testing.T) {
	ca, other := newPKI(t, "test CA"), newPKI(t, "other CA")
	srv := serveTLS(t, ca, ca)
	tlsEnv(t, srv, certDir(t, ca, other)) // a client certificate the daemon does not trust
	if _, err := summarize(t); err == nil {
		t.Fatal("a client certificate from another authority must be refused")
	}
}

func TestTLSWithAMissingCAIsAnError(t *testing.T) {
	ca := newPKI(t, "test CA")
	srv := serveTLS(t, ca, nil)
	tlsEnv(t, srv, t.TempDir()) // an empty certificate directory
	_, err := summarize(t)
	if err == nil || !strings.Contains(err.Error(), "CA") {
		t.Fatalf("err = %v, want it to say that the CA cannot be read", err)
	}
}

// A docker context carries its own TLS material and skip-tls-verify flag.
func TestTLSFromADockerContext(t *testing.T) {
	ca := newPKI(t, "test CA")
	srv := serveTLS(t, ca, ca)
	config := t.TempDir()
	t.Setenv("DOCKER_CONFIG", config)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CONTEXT", "")

	host := "tcp://" + srv.Listener.Addr().String()
	writeContext(t, config, "prod", host)
	tlsDir := filepath.Join(config, "contexts", "tls", contextID("prod"), "docker")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cert, key := ca.issue(t, "operator", x509.ExtKeyUsageClientAuth)
	for name, data := range map[string][]byte{"ca.pem": ca.caPEM, "cert.pem": cert, "key.pem": key} {
		if err := os.WriteFile(filepath.Join(tlsDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := Connect(ConnectOptions{Context: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if sum, err := a.Summary(context.Background()); err != nil || sum.Context != "prod" || sum.NodeName != "vm" {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
}

func TestSkipTLSVerifyOfAContext(t *testing.T) {
	impostor := newPKI(t, "impostor CA")
	srv := serveTLS(t, impostor, nil)
	config := t.TempDir()
	t.Setenv("DOCKER_CONFIG", config)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CONTEXT", "")

	host := "tcp://" + srv.Listener.Addr().String()
	for skip, wantErr := range map[bool]bool{false: true, true: false} {
		name := map[bool]string{false: "strict", true: "lax"}[skip]
		metaDir := filepath.Join(config, "contexts", "meta", contextID(name))
		if err := os.MkdirAll(metaDir, 0o700); err != nil {
			t.Fatal(err)
		}
		meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `","SkipTLSVerify":` + map[bool]string{false: "false", true: "true"}[skip] + `}}}`
		if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
		a, err := Connect(ConnectOptions{Context: name})
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.Summary(context.Background())
		_ = a.Close()
		if (err != nil) != wantErr {
			t.Errorf("context %s: err = %v, want an error: %v", name, err, wantErr)
		}
	}
}
