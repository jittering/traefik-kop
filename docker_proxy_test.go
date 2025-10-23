package traefikkop

import (
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
	assertServiceIPs(t, store, []svc{
		{"hello-detect", "http", "http://192.168.100.100:5577"},
		{"hello-detect2", "http", "http://192.168.100.100:5577"},
	})
}
