package traefikkop

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

func TestMultiPortPublishedConfiguration(t *testing.T) {
	store := processFile(t, "multi-port.yml")
	assertServiceIPs(t, store, []svc{
		{"nginx", "http", "http://192.168.100.100:12345"},
		{"nginx-tcp", "tcp", "192.168.100.100:2222"},
		{"nginx-tcp-random", "tcp", "192.168.100.100:12346"},
		{"nginx-udp", "udp", "192.168.100.100:1000"},
		{"nginx-udp-other", "udp", "192.168.100.100:9000"},
		{"nginx-udp-random", "udp", "192.168.100.100:12347"},
	})
}

func TestServicePortResolution(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, label, inferred, want string
	}{
		{"HTTP internal label", "http", "80", "80", "32768"},
		{"TCP internal label", "tcp", "80", "80", "32768"},
		{"UDP same internal number", "udp", "80", "80", "32769"},
		{"inferred internal port", "http", "", "80", "32768"},
		{"explicit host port", "http", "8080", "8080", "8080"},
		{"unpublished explicit port", "tcp", "443", "443", "443"},
		{"wrong protocol", "udp", "22", "22", "22"},
		{"unbound port", "http", "90", "90", "90"},
		{"static host binding", "tcp", "22", "22", "2222"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{"traefik." + tc.protocol + ".services.nginx.loadbalancer.server.port": tc.label}
			client := createTestClient(labels)
			client.container.HostConfig.PortBindings = nat.PortMap{
				"80/tcp": {{HostPort: "0"}},
				"80/udp": {{HostPort: ""}},
				"22/tcp": {{HostPort: "2222"}},
				"90/tcp": nil,
			}
			client.container.NetworkSettings = &types.NetworkSettings{
				NetworkSettingsBase: types.NetworkSettingsBase{Ports: nat.PortMap{
					"80/tcp": {{HostIP: "0.0.0.0", HostPort: "32768"}, {HostIP: "::", HostPort: "32768"}},
					"80/udp": {{HostPort: "32769"}},
					"90/tcp": nil,
				}},
			}
			conf := &dynamic.Configuration{HTTP: &dynamic.HTTPConfiguration{}, TCP: &dynamic.TCPConfiguration{}, UDP: &dynamic.UDPConfiguration{}}
			got := getContainerPort(&dockerCache{client: client}, conf, tc.protocol, "nginx@docker", tc.inferred)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestReplaceIPsResolvesInferredPorts(t *testing.T) {
	client := createTestClient(map[string]string{
		"traefik.http.services.nginx.loadbalancer.server.scheme": "http",
		"traefik.tcp.routers.nginx.service":                      "nginx",
		"traefik.udp.routers.nginx.service":                      "nginx",
	})
	client.container.HostConfig.PortBindings = nat.PortMap{
		"80/tcp": {{HostPort: "8080"}},
		"80/udp": {{HostPort: "9080"}},
	}
	conf := &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{Services: map[string]*dynamic.Service{
			"nginx@docker": {LoadBalancer: &dynamic.ServersLoadBalancer{Servers: []dynamic.Server{
				{URL: "http://172.18.0.2:80"},
				{Scheme: "http", Port: "80"},
			}}},
		}},
		TCP: &dynamic.TCPConfiguration{
			Routers: map[string]*dynamic.TCPRouter{"nginx": {Service: "nginx"}},
			Services: map[string]*dynamic.TCPService{
				"nginx@docker": {LoadBalancer: &dynamic.TCPServersLoadBalancer{Servers: []dynamic.TCPServer{{Address: "172.18.0.2:80"}}}},
			},
		},
		UDP: &dynamic.UDPConfiguration{
			Routers: map[string]*dynamic.UDPRouter{"nginx": {Service: "nginx"}},
			Services: map[string]*dynamic.UDPService{
				"nginx@docker": {LoadBalancer: &dynamic.UDPServersLoadBalancer{Servers: []dynamic.UDPServer{{Address: "172.18.0.2:80"}}}},
			},
		},
	}
	replaceIPs(&dockerCache{client: client}, conf, "192.168.100.100")
	for _, server := range conf.HTTP.Services["nginx@docker"].LoadBalancer.Servers {
		require.Equal(t, "http://192.168.100.100:8080", server.URL)
	}
	require.Equal(t, "192.168.100.100:8080", conf.TCP.Services["nginx@docker"].LoadBalancer.Servers[0].Address)
	require.Equal(t, "192.168.100.100:9080", conf.UDP.Services["nginx@docker"].LoadBalancer.Servers[0].Address)
}
