package traefikkop

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPKI struct {
	caFile, serverCert, serverKey, clientCert, clientKey string
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600))
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	issue := func(serial int64, cn string, usage x509.ExtKeyUsage) (certDER []byte, keyDER []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
			DNSNames:     []string{"redis.test"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		}
		certDER, err = x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		require.NoError(t, err)
		keyDER, err = x509.MarshalECPrivateKey(key)
		require.NoError(t, err)
		return
	}

	p := testPKI{
		caFile:     filepath.Join(dir, "ca.pem"),
		serverCert: filepath.Join(dir, "server.pem"),
		serverKey:  filepath.Join(dir, "server.key"),
		clientCert: filepath.Join(dir, "client.pem"),
		clientKey:  filepath.Join(dir, "client.key"),
	}
	writePEM(t, p.caFile, "CERTIFICATE", caDER)
	c, k := issue(2, "redis", x509.ExtKeyUsageServerAuth)
	writePEM(t, p.serverCert, "CERTIFICATE", c)
	writePEM(t, p.serverKey, "EC PRIVATE KEY", k)
	c, k = issue(3, "kop", x509.ExtKeyUsageClientAuth)
	writePEM(t, p.clientCert, "CERTIFICATE", c)
	writePEM(t, p.clientKey, "EC PRIVATE KEY", k)
	return p
}

func TestRedisTLSConfigBuild(t *testing.T) {
	pki := newTestPKI(t)

	t.Run("disabled returns nil", func(t *testing.T) {
		conf, err := RedisTLSConfig{}.Build()
		require.NoError(t, err)
		assert.Nil(t, conf)
	})

	t.Run("options without enable is an error", func(t *testing.T) {
		_, err := RedisTLSConfig{CAFile: pki.caFile}.Build()
		assert.Error(t, err)
	})

	t.Run("full mtls config", func(t *testing.T) {
		conf, err := RedisTLSConfig{
			Enabled: true, CAFile: pki.caFile, CertFile: pki.clientCert, KeyFile: pki.clientKey, ServerName: "redis.test",
		}.Build()
		require.NoError(t, err)
		assert.NotNil(t, conf.RootCAs)
		assert.Len(t, conf.Certificates, 1)
		assert.Equal(t, "redis.test", conf.ServerName)
		assert.False(t, conf.InsecureSkipVerify)
		assert.GreaterOrEqual(t, int(conf.MinVersion), tls.VersionTLS12)
	})

	t.Run("cert without key", func(t *testing.T) {
		_, err := RedisTLSConfig{Enabled: true, CertFile: pki.clientCert}.Build()
		assert.Error(t, err)
	})

	t.Run("missing ca file", func(t *testing.T) {
		_, err := RedisTLSConfig{Enabled: true, CAFile: filepath.Join(t.TempDir(), "nope.pem")}.Build()
		assert.Error(t, err)
	})

	t.Run("garbage ca file", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.pem")
		require.NoError(t, os.WriteFile(bad, []byte("not a cert"), 0o600))
		_, err := RedisTLSConfig{Enabled: true, CAFile: bad}.Build()
		assert.Error(t, err)
	})

	t.Run("bad key pair", func(t *testing.T) {
		_, err := RedisTLSConfig{Enabled: true, CertFile: pki.clientCert, KeyFile: pki.serverKey}.Build()
		assert.Error(t, err)
	})
}

// Runs against a TLS-only miniredis that requires client certificates.
func TestRedisStoreMutualTLS(t *testing.T) {
	pki := newTestPKI(t)

	serverPair, err := tls.LoadX509KeyPair(pki.serverCert, pki.serverKey)
	require.NoError(t, err)
	caPool := x509.NewCertPool()
	caPEM, err := os.ReadFile(pki.caFile)
	require.NoError(t, err)
	require.True(t, caPool.AppendCertsFromPEM(caPEM))

	server := miniredis.NewMiniRedis()
	require.NoError(t, server.StartTLS(&tls.Config{
		Certificates: []tls.Certificate{serverPair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}))
	defer server.Close()

	t.Run("with client cert", func(t *testing.T) {
		conf, err := RedisTLSConfig{
			Enabled: true, CAFile: pki.caFile, CertFile: pki.clientCert, KeyFile: pki.clientKey, ServerName: "redis.test",
		}.Build()
		require.NoError(t, err)
		store := NewRedisStore("localhost", server.Addr(), 0, "", "", 0, nil, "", conf)
		assert.NoError(t, store.Ping())
	})

	t.Run("without client cert", func(t *testing.T) {
		conf, err := RedisTLSConfig{Enabled: true, CAFile: pki.caFile, ServerName: "redis.test"}.Build()
		require.NoError(t, err)
		store := NewRedisStore("localhost", server.Addr(), 0, "", "", 0, nil, "", conf)
		assert.Error(t, store.Ping())
	})

	t.Run("untrusted server", func(t *testing.T) {
		conf, err := RedisTLSConfig{Enabled: true, CertFile: pki.clientCert, KeyFile: pki.clientKey}.Build()
		require.NoError(t, err)
		store := NewRedisStore("localhost", server.Addr(), 0, "", "", 0, nil, "", conf)
		assert.Error(t, store.Ping())
	})
}
