package traefikkop

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/docker/docker/client"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

var app *fiber.App
var dockerEndpoint string
var dc client.APIClient
var dockerAPI = &DockerAPIStub{}

func setup() {
	app, dockerEndpoint = createHTTPServer()
	var err error
	dc, err = createDockerClient(dockerEndpoint)
	if err != nil {
		log.Fatal(err)
	}
}

func teardown() {
	err := app.Shutdown()
	if err != nil {
		log.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	setup()

	code := m.Run()

	teardown()

	os.Exit(code)
}

func Test_httpServerVersion(t *testing.T) {
	v, err := dc.ServerVersion(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "1.0.0", v.Version)
}

func Test_helloWorld(t *testing.T) {
	store := processFile(t, "helloworld.yml")

	assert.NotNil(t, store)
	assert.NotNil(t, store.kv)

	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assert.Equal(t, "hello2", store.kv["traefik/http/routers/hello2/service"])
	assert.NotNil(t, store.kv["traefik/http/routers/hello1/tls/certResolver"])
	assert.NotNil(t, store.kv["traefik/http/routers/hello2/tls/certResolver"])

	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
		{"hello2", "http", "http://192.168.100.100:5566"},
	})

	// assertServiceIP(t, store, "hello1", "http://192.168.100.100:5555")
	// assert.Equal(t, "http://192.168.100.100:5555", store.kv["traefik/http/services/hello1/loadBalancer/servers/0/url"])
	// assert.Equal(t, "http://192.168.100.100:5566", store.kv["traefik/http/services/hello2/loadBalancer/servers/0/url"])
}

func Test_helloDetect(t *testing.T) {
	// both services get mapped to the same port (error case)
	store := processFile(t, "hellodetect.yml")
	assertServiceIPs(t, store, []svc{
		{"hello-detect", "http", "http://192.168.100.100:5577"},
		{"hello-detect2", "http", "http://192.168.100.100:5577"},
	})
}

func Test_helloIP(t *testing.T) {
	// override ip via labels
	store := processFile(t, "helloip.yml")
	assertServiceIPs(t, store, []svc{
		{"helloip", "http", "http://4.4.4.4:5599"},
		{"helloip2", "http", "http://3.3.3.3:5599"},
	})
}

func Test_helloNetwork(t *testing.T) {
	// use ip from specific docker network
	store := processFile(t, "network.yml")
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://10.10.10.5:5555"},
	})
}

func Test_TCP(t *testing.T) {
	// tcp service
	store := processFile(t, "gitea.yml")
	assertServiceIPs(t, store, []svc{
		{"gitea-ssh", "tcp", "192.168.100.100:20022"},
	})
}

func Test_TCPMQTT(t *testing.T) {
	// from https://github.com/jittering/traefik-kop/issues/35
	store := processFile(t, "mqtt.yml")
	assertServiceIPs(t, store, []svc{
		{"mqtt", "http", "http://192.168.100.100:9001"},
		{"mqtt", "tcp", "192.168.100.100:1883"},
	})
}

func Test_helloWorldNoCert(t *testing.T) {
	store := processFile(t, "hello-no-cert.yml")

	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assert.Nil(t, store.kv["traefik/http/routers/hello1/tls/certResolver"])

	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})
}

func Test_helloWorldIgnore(t *testing.T) {
	store := processFile(t, "hello-ignore.yml")
	assert.Nil(t, store.kv["traefik/http/routers/hello1/service"])

	store = processFileWithConfig(t, nil, &Config{Namespace: []string{"foobar"}}, "hello-ignore.yml")
	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})
}

func Test_helloWorldMultiNS(t *testing.T) {
	store := processFile(t, "hello-multi-ns.yml")
	assert.Nil(t, store.kv["traefik/http/routers/hello1/service"])

	store = processFileWithConfig(t, nil, &Config{Namespace: []string{"foobar"}}, "hello-multi-ns.yml")
	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})

	store = processFileWithConfig(t, nil, &Config{Namespace: []string{"xyz"}}, "hello-multi-ns.yml")
	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})

	store = processFileWithConfig(t, nil, &Config{Namespace: []string{"foobar", "xyz"}}, "hello-multi-ns.yml")
	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})

	store = processFileWithConfig(t, nil, &Config{Namespace: []string{"abc"}}, "hello-multi-ns.yml")
	assert.Nil(t, store.kv["traefik/http/routers/hello1/service"])
}

func Test_helloWorldAutoMapped(t *testing.T) {
	store := processFile(t, "hello-automapped.yml")
	assert.Equal(t, "hello", store.kv["traefik/http/routers/hello/service"])
	assertServiceIPs(t, store, []svc{
		{"hello", "http", "http://192.168.100.100:12345"},
	})
}

func Test_samePrefix(t *testing.T) {
	store := processFile(t, "prefix.yml")

	// Two services `hello` and `hello-test`.
	// The former's name is a prefix of the latter. Ensure the matching does not mix them up.
	assertServiceIPs(t, store, []svc{
		{"hello", "http", "http://192.168.100.100:5555"},
		{"hello-test", "http", "http://192.168.100.100:5566"},
	})
}

// Should be able to merge loadbalanced services
func Test_loadbalance(t *testing.T) {
	store := processFile(t, "loadbalance1.yml", "loadbalance2.yml")

	url1 := store.kv["traefik/http/services/lbtest/loadBalancer/servers/0/url"]
	url2 := store.kv["traefik/http/services/lbtest/loadBalancer/servers/1/url"]

	assert.NotNil(t, url1)
	assert.NotNil(t, url2)

	urls := []string{
		url1.(string),
		url2.(string),
	}

	assert.Contains(t, urls, "http://192.168.100.100:5555")
	assert.Contains(t, urls, "http://192.168.100.100:5556")
}

func Test_mixedLabelsFiltering(t *testing.T) {
	// container has both traefik.* and kop.$namespace.traefik.* labels
	// Only services/routers generated from kop labels should be kept
	store := processFileWithConfig(t, nil, &Config{Namespace: []string{"ns1"}}, "hello-kop-prefixed.yaml")

	// kop-generated should exist
	assert.Equal(t, "hello-internal", store.kv["traefik/http/routers/hello-internal/service"])
	assert.Contains(t, store.kv["traefik/http/services/hello-internal/loadBalancer/servers/0/url"], ":23001")

	// local traefik-only should be filtered out by kop instance
	assert.Nil(t, store.kv["traefik/http/routers/hello-external/service"])
	assert.Nil(t, store.kv["traefik/http/services/hello-external/loadBalancer/servers/0/url"])
}
