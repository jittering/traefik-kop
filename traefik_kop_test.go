package traefikkop

import (
	"encoding/json"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
)

func Test_replaceIPs(t *testing.T) {
	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)
	require.Contains(t, cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2")
	replaceIPs(cfg, "7.7.7.7")
	require.NotContains(t, cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2")
	require.Equal(t, "http://7.7.7.7:80", cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL)

	cfg = &dynamic.Configuration{}
	_, err = toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)
	require.Equal(t, "foobar", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
	replaceIPs(cfg, "7.7.7.7")
	require.Equal(t, "7.7.7.7", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
}
