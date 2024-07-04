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
	store := doTest(t, "helloworld.yml", nil)

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
	store := doTest(t, "hellodetect.yml", nil)
	assertServiceIPs(t, store, []svc{
		{"hello-detect", "http", "http://192.168.100.100:5577"},
		{"hello-detect2", "http", "http://192.168.100.100:5577"},
	})
}

func Test_helloIP(t *testing.T) {
	// override ip via labels
	store := doTest(t, "helloip.yml", nil)
	assertServiceIPs(t, store, []svc{
		{"helloip", "http", "http://4.4.4.4:5599"},
		{"helloip2", "http", "http://3.3.3.3:5599"},
	})
}

func Test_helloNetwork(t *testing.T) {
	// use ip from specific docker network
	store := doTest(t, "network.yml", nil)
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://10.10.10.5:5555"},
	})
}

func Test_TCP(t *testing.T) {
	// tcp service
	store := doTest(t, "gitea.yml", nil)
	assertServiceIPs(t, store, []svc{
		{"gitea-ssh", "tcp", "192.168.100.100:20022"},
	})
}

func Test_TCPMQTT(t *testing.T) {
	// from https://github.com/jittering/traefik-kop/issues/35
	store := doTest(t, "mqtt.yml", nil)
	assertServiceIPs(t, store, []svc{
		{"mqtt", "http", "http://192.168.100.100:9001"},
		{"mqtt", "tcp", "192.168.100.100:1883"},
	})
}

func Test_helloWorldNoCert(t *testing.T) {
	store := doTest(t, "hello-no-cert.yml", nil)

	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assert.Nil(t, store.kv["traefik/http/routers/hello1/tls/certResolver"])

	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})
}

func Test_helloWorldIgnore(t *testing.T) {
	store := doTest(t, "hello-ignore.yml", nil)
	assert.Nil(t, store.kv["traefik/http/routers/hello1/service"])

	store = doTest(t, "hello-ignore.yml", &Config{Namespace: "foobar"})
	assert.Equal(t, "hello1", store.kv["traefik/http/routers/hello1/service"])
	assertServiceIPs(t, store, []svc{
		{"hello1", "http", "http://192.168.100.100:5555"},
	})
}
