package traefikkop

import (
	"encoding/json"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

const NGINX_CONF_JSON = `{"http":{"routers":{"nginx@docker":{"service":"nginx","rule":"Host('nginx.local')"}},"services":{"nginx@docker":{"loadBalancer":{"servers":[{"url":"http://172.20.0.2:80"}],"passHostHeader":true}}}},"tcp":{},"udp":{},"tls":{"options":{"default":{"clientAuth":{},"alpnProtocols":["h2","http/1.1","acme-tls/1"]}}}}`
const NGINX_CONF_JSON_DIFFRENT_SERVICE_NAME = `{"http":{"routers":{"nginx@docker":{"service":"nginx-nginx","rule":"Host('nginx.local')"}},"services":{"nginx-nginx@docker":{"loadBalancer":{"servers":[{"url":"http://172.20.0.2:80"}],"passHostHeader":true}}}},"tcp":{},"udp":{},"tls":{"options":{"default":{"clientAuth":{},"alpnProtocols":["h2","http/1.1","acme-tls/1"]}}}}`

func Test_collectKeys(t *testing.T) {
	cfg := &dynamic.Configuration{}
	_, err := toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)

	keys := collectKeys(cfg.HTTP.Middlewares)
	require.NotEmpty(t, keys)
	require.Contains(t, keys, "Middleware21")

	require.Contains(t, collectKeys(cfg.HTTP.Services), "Service0")

	cfg = &dynamic.Configuration{}
	err = json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)
	keys = collectKeys(cfg.HTTP.Routers)
	require.Len(t, keys, 1)
}

// keys := collectKeys(cfg.HTTP.Middlewares)
// require.NotEmpty(t, keys)
// require.True(t, keys.Contains("Middleware21"))

// require.True(t, collectKeys(cfg.HTTP.Services).Contains("Service0"))
