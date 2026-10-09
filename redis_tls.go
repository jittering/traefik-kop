package traefikkop

import (
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/pkg/errors"
)

// RedisTLSConfig holds the settings for connecting to Redis over TLS
// (optionally with a client certificate for mutual TLS).
type RedisTLSConfig struct {
	Enabled            bool
	CAFile             string // PEM bundle used to verify the server; system roots if empty
	CertFile           string // client certificate (mTLS)
	KeyFile            string // client private key (mTLS)
	ServerName         string // override the name used to verify the server certificate
	InsecureSkipVerify bool
}

// Build returns the tls.Config for the redis client, or nil when TLS is not
// enabled. Bad or unreadable files are reported as errors so the caller can
// fail at startup.
func (c RedisTLSConfig) Build() (*tls.Config, error) {
	if !c.Enabled {
		if c.CAFile != "" || c.CertFile != "" || c.KeyFile != "" || c.ServerName != "" || c.InsecureSkipVerify {
			return nil, errors.New("redis TLS options were set but --redis-tls is not enabled")
		}
		return nil, nil
	}

	if (c.CertFile == "") != (c.KeyFile == "") {
		return nil, errors.New("--redis-tls-cert and --redis-tls-key must be provided together")
	}

	conf := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         c.ServerName,
		InsecureSkipVerify: c.InsecureSkipVerify, //nolint:gosec // explicit opt-in
	}

	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read redis TLS CA file %s", c.CAFile)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.Errorf("no valid certificates found in redis TLS CA file %s", c.CAFile)
		}
		conf.RootCAs = pool
	}

	if c.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, errors.Wrap(err, "failed to load redis TLS client certificate/key")
		}
		conf.Certificates = []tls.Certificate{cert}
	}

	return conf, nil
}
