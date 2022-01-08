package traefikkop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
)

type fakeDockerClient struct {
	client.APIClient
	containers []types.Container
	container  types.ContainerJSON
	err        error
}

func (c *fakeDockerClient) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	return c.containers, nil
}

func (c *fakeDockerClient) ContainerInspect(ctx context.Context, container string) (types.ContainerJSON, error) {
	return c.container, c.err
}

func Test_replaceIPs(t *testing.T) {
	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)
	require.Contains(t, cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2")

	// replace and test check again
	replaceIPs(&fakeDockerClient{}, cfg, "7.7.7.7")
	require.NotContains(t, cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2")

	// full url
	require.Equal(t, "http://7.7.7.7:80", cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL)

	// test again with larger fixture, tcp sservice
	cfg = &dynamic.Configuration{}
	_, err = toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)
	require.Equal(t, "foobar", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
	replaceIPs(&fakeDockerClient{}, cfg, "7.7.7.7")
	require.Equal(t, "7.7.7.7", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
}
