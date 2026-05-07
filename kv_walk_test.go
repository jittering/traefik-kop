package traefikkop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ptypes "github.com/traefik/paerser/types"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

func TestConfigToKVAllowEmptyLabel(t *testing.T) {
	cfg := dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Routers: map[string]*dynamic.Router{
				"musicassistant@docker": {
					Rule:        "Host(`musicassistant.example.com`)",
					Service:     "musicassistant",
					EntryPoints: []string{"websecure"},
					TLS:         &dynamic.RouterTLSConfig{},
				},
			},
		},
		TCP: &dynamic.TCPConfiguration{},
		UDP: &dynamic.UDPConfiguration{},
		TLS: &dynamic.TLSConfiguration{},
	}

	got, err := ConfigToKV(cfg, 3)
	require.NoError(t, err)
	require.Equal(t, "true", got["traefik/http/routers/musicassistant/tls"])
}

func TestConfigToKVUsesJSONLeafEncoding(t *testing.T) {
	cfg := dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Services: map[string]*dynamic.Service{
				"musicassistant@docker": {
					LoadBalancer: &dynamic.ServersLoadBalancer{
						ResponseForwarding: &dynamic.ResponseForwarding{
							FlushInterval: ptypes.Duration(5 * time.Second),
						},
					},
				},
			},
		},
		TCP: &dynamic.TCPConfiguration{},
		UDP: &dynamic.UDPConfiguration{},
		TLS: &dynamic.TLSConfiguration{},
	}

	got, err := ConfigToKV(cfg, 3)
	require.NoError(t, err)
	require.Equal(t, "5s", got["traefik/http/services/musicassistant/loadBalancer/responseForwarding/flushInterval"])
}
