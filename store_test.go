package traefikkop

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/alicebob/miniredis/v2"
	"github.com/echovault/sugardb/sugardb"
	"github.com/stretchr/testify/assert"
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

func Test_redisStore(t *testing.T) {
	l, err := getAvailablePort()
	assert.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	assert.NoError(t, l.Close())

	server, err := sugardb.NewSugarDB(sugardb.WithPort(uint16(port)))
	if err != nil {
		assert.NoError(t, err)
	}

	go server.Start()
	defer server.ShutDown()

	store := NewRedisStore("localhost", fmt.Sprintf("localhost:%d", port), 0, "", "", 0, nil, "")
	processFileWithConfig(t, store, nil, "hellodetect.yml")
	assertServiceIPs(t, store, []svc{
		{"hello-detect", "http", "http://192.168.100.100:5577"},
		{"hello-detect2", "http", "http://192.168.100.100:5577"},
	})
}

func Test_redisStoreRemovesStaleServerKeysOnUpdate(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	store := NewRedisStore("localhost", server.Addr(), 0, "", "", 0, nil, "")

	initial := &dynamic.Configuration{}
	err = json.Unmarshal([]byte(`{
		"http": {
			"services": {
				"app@docker": {
					"loadBalancer": {
						"servers": [
							{"url": "http://10.0.0.1:8080"},
							{"url": "http://10.0.0.2:8080"}
						]
					}
				}
			}
		},
		"tcp": {},
		"udp": {}
	}`), initial)
	require.NoError(t, err)
	require.NoError(t, store.Store(*initial))

	staleKey := "traefik/http/services/app/loadBalancer/servers/1/url"
	val, err := store.Get(staleKey)
	require.NoError(t, err)
	require.Equal(t, "http://10.0.0.2:8080", val)

	updated := &dynamic.Configuration{}
	err = json.Unmarshal([]byte(`{
		"http": {
			"services": {
				"app@docker": {
					"loadBalancer": {
						"servers": [
							{"url": "http://10.0.0.3:8080"}
						]
					}
				}
			}
		},
		"tcp": {},
		"udp": {}
	}`), updated)
	require.NoError(t, err)
	require.NoError(t, store.Store(*updated))

	remaining, err := store.Get("traefik/http/services/app/loadBalancer/servers/0/url")
	require.NoError(t, err)
	require.Equal(t, "http://10.0.0.3:8080", remaining)

	stale, err := store.Get(staleKey)
	require.NoError(t, err)
	require.Empty(t, stale)
}
