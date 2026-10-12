package traefikkop

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

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

	store := NewRedisStore("localhost", fmt.Sprintf("localhost:%d", port), 0, "", "", 0, nil, "", nil)
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

	store := NewRedisStore("localhost", server.Addr(), 0, "", "", 0, nil, "", nil)

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

func newTTLTestStore(t *testing.T, ttl int) (*miniredis.Miniredis, *RedisStore, dynamic.Configuration) {
	t.Helper()
	server := miniredis.RunT(t)
	store := NewRedisStore("ttl-test", server.Addr(), ttl, "", "", 0, nil, "", nil).(*RedisStore)
	t.Cleanup(func() { require.NoError(t, store.client.Close()) })
	var conf dynamic.Configuration
	require.NoError(t, json.Unmarshal([]byte(NGINX_CONF_JSON), &conf))
	return server, store, conf
}

func TestRedisTTLRenewal(t *testing.T) {
	server, store, conf := newTTLTestStore(t, 300)
	require.NoError(t, store.KeepConfAlive()) // No configuration yet.
	require.Empty(t, server.Keys())
	require.NoError(t, store.Store(conf))
	kv, err := ConfigToKV(conf)
	require.NoError(t, err)
	require.NoError(t, server.Set("traefik/http/routers/other/rule", "other"))
	server.SetTTL("traefik/http/routers/other/rule", 300*time.Second)

	// Cross several original TTL boundaries without losing any published keys.
	for i := 0; i < 10; i++ {
		server.FastForward(100 * time.Second)
		for key := range kv {
			require.True(t, server.Exists(key), key)
		}
		require.NoError(t, store.KeepConfAlive())
		for key := range kv {
			require.Equal(t, 300*time.Second, server.TTL(key), key)
		}
		require.Equal(t, 300*time.Second, server.TTL(store.sk("kop_last_update")))
	}
	require.False(t, server.Exists("traefik/http/routers/other/rule"))

	// With no further renewal, routes and sentinel still expire.
	server.FastForward(300 * time.Second)
	for key := range kv {
		require.False(t, server.Exists(key), key)
	}
	require.False(t, server.Exists(store.sk("kop_last_update")))
}

func TestRedisKeepAliveRecovery(t *testing.T) {
	for _, ttl := range []int{0, 300} {
		t.Run(fmt.Sprint(ttl), func(t *testing.T) {
			server, store, conf := newTTLTestStore(t, ttl)
			require.NoError(t, store.Store(conf))
			server.FlushAll()
			require.NoError(t, store.KeepConfAlive())
			key := "traefik/http/services/nginx/loadBalancer/servers/0/url"
			require.True(t, server.Exists(key))
			require.Equal(t, time.Duration(ttl)*time.Second, server.TTL(key))
			if ttl > 0 {
				server.Del(key) // Also recover partial key loss with a live sentinel.
				require.NoError(t, store.KeepConfAlive())
				require.True(t, server.Exists(key))
			}
			server.SetError("ERR unavailable")
			require.Error(t, store.KeepConfAlive())
		})
	}
}

func TestRedisTTLTimerWithoutDockerPolling(t *testing.T) {
	server, store, conf := newTTLTestStore(t, 1)
	require.NoError(t, store.Store(conf))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepStoreAlive(ctx, store, store.TTL/3)
	}()
	t.Cleanup(func() { cancel(); <-done })
	key := "traefik/http/services/nginx/loadBalancer/servers/0/url"
	for i := 0; i < 4; i++ {
		server.FastForward(600 * time.Millisecond)
		require.True(t, server.Exists(key))
		require.Eventually(t, func() bool {
			return server.TTL(key) == time.Second
		}, time.Second, 10*time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("renewal timer did not stop")
	}
	server.FastForward(time.Second)
	require.False(t, server.Exists(key))
}

func TestRedisTTLRenewalWithConfigurationChanges(t *testing.T) {
	server, store, conf := newTTLTestStore(t, 300)
	require.NoError(t, store.Store(conf))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			assert.NoError(t, store.KeepConfAlive())
		}
	}()
	updated := conf.DeepCopy()
	delete(updated.HTTP.Services, "nginx@docker")
	delete(updated.HTTP.Routers, "nginx@docker")
	require.NoError(t, store.Store(*updated))
	wg.Wait()
	require.NoError(t, store.KeepConfAlive())
	require.False(t, server.Exists("traefik/http/services/nginx/loadBalancer/servers/0/url"))
}
