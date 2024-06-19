package traefikkop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/log"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	log.SetLevel(logrus.DebugLevel)
	log.WithoutContext().WriterLevel(logrus.DebugLevel)
}

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

	// test again with larger fixture, tcp service
	cfg = &dynamic.Configuration{}
	_, err = toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)
	require.Equal(t, "foobar", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
	replaceIPs(&fakeDockerClient{}, cfg, "7.7.7.7")
	require.Equal(t, "7.7.7.7", cfg.TCP.Services["TCPService0"].LoadBalancer.Servers[0].Address)
}

func createTestClient(labels map[string]string) *fakeDockerClient {
	return &fakeDockerClient{
		containers: []types.Container{
			types.Container{
				ID: "foobar_id",
			},
		},
		container: types.ContainerJSON{
			ContainerJSONBase: &types.ContainerJSONBase{
				ID:         "foobar_id",
				HostConfig: &container.HostConfig{},
			},
			Config: &container.Config{
				Labels: labels,
			},
		},
	}

}

func Test_replacePorts(t *testing.T) {

	portMap := nat.PortMap{
		"80": []nat.PortBinding{
			{HostIP: "172.20.0.2", HostPort: "8888"},
		},
	}

	portLabel := "traefik.http.services.nginx.loadbalancer.server.port"
	dc := createTestClient(map[string]string{
		"traefik.http.services.nginx.loadbalancer.server.scheme": "http",
		portLabel: "8888",
	})

	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2:80"))

	// explicit label present
	replaceIPs(dc, cfg, "4.4.4.4")
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"), "URL '%s' should end with '%s'", cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888")

	// without label but no port binding
	delete(dc.container.Config.Labels, portLabel)
	json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	replaceIPs(dc, cfg, "4.4.4.4")
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))

	// with port binding
	dc.container.HostConfig.PortBindings = portMap
	json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	replaceIPs(dc, cfg, "4.4.4.4")
	require.False(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))
}

func Test_replacePortsNoService(t *testing.T) {

	portMap := nat.PortMap{
		"80": []nat.PortBinding{
			{HostIP: "172.20.0.2", HostPort: "8888"},
		},
	}

	dc := createTestClient(map[string]string{
		"traefik.http.routers.nginx.entrypoints": "web-secure",
	})

	cfg := &dynamic.Configuration{}
	err := json.Unmarshal([]byte(NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME), cfg)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "172.20.0.2:80"))

	// explicit label present
	replaceIPs(dc, cfg, "4.4.4.4")
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))

	// without label but no port binding
	json.Unmarshal([]byte(NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME), cfg)
	replaceIPs(dc, cfg, "4.4.4.4")
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))

	// with port binding
	dc.container.HostConfig.PortBindings = portMap
	json.Unmarshal([]byte(NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME), cfg)
	replaceIPs(dc, cfg, "4.4.4.4")
	require.False(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:80"))
	require.True(t, strings.HasSuffix(cfg.HTTP.Services["nginx-nginx@docker"].LoadBalancer.Servers[0].URL, "4.4.4.4:8888"))
}
