package traefikkop

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_dockerProxyServerNoPrefix(t *testing.T) {
	mockDockerEndpoint := dockerEndpoint
	mockDockerClient := dc

	// now create our proxy pointing to the mock
	proxyServer := createProxy(mockDockerClient, "")
	_, proxyDockerEndpoint := proxyServer.start()

	var err error
	dockerEndpoint = proxyDockerEndpoint
	dc, err = createDockerClient(proxyDockerEndpoint)
	assert.NoError(t, err, "should create docker client")
	defer func() {
		dockerEndpoint = mockDockerEndpoint
		dc = mockDockerClient
	}()

	// both services get mapped to the same port (error case)
	store := processFile(t, "hellodetect.yml")
	processFileWithConfig(t, store, nil, "docker-prefix.yml")
	assertServiceIPs(t, store, []svc{
		{"hello-detect", "http", "http://192.168.100.100:5577"},
		{"hello-detect2", "http", "http://192.168.100.100:5577"},
	})
	assert.NotEmpty(t, store.kv[fmt.Sprintf("traefik/http/routers/%s/service", "hello-detect2")])
	assert.Empty(t, store.kv[fmt.Sprintf("traefik/http/routers/%s/service", "prefixed")])
}

func Test_dockerProxyServerPrefix(t *testing.T) {
	mockDockerEndpoint := dockerEndpoint
	mockDockerClient := dc

	// now create our proxy pointing to the mock
	proxyServer := createProxy(mockDockerClient, "foo")
	_, proxyDockerEndpoint := proxyServer.start()

	var err error
	dockerEndpoint = proxyDockerEndpoint
	dc, err = createDockerClient(proxyDockerEndpoint)
	assert.NoError(t, err, "should create docker client")
	defer func() {
		dockerEndpoint = mockDockerEndpoint
		dc = mockDockerClient
	}()

	// both services get mapped to the same port (error case)
	store := processFile(t, "hellodetect.yml")
	processFileWithConfig(t, store, nil, "docker-prefix.yml")

	assertServiceIPs(t, store, []svc{
		{"prefixed", "http", "http://192.168.100.100:5588"},
	})

	assert.Empty(t, store.kv[fmt.Sprintf("traefik/http/routers/%s/service", "hello-detect2")])
	assert.NotEmpty(t, store.kv[fmt.Sprintf("traefik/http/routers/%s/service", "prefixed")])
}
