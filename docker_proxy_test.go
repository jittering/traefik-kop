package traefikkop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
)

// proxyStubClient stubs only the APIClient methods used by the proxy when
// testing the exposed-by-default filtering
type proxyStubClient struct {
	client.APIClient
	containers  []container.Summary
	eventsCh    chan events.Message
	lastListAll bool
}

func (s *proxyStubClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	s.lastListAll = options.All
	return s.containers, nil
}

func (s *proxyStubClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	return s.eventsCh, make(chan error)
}

func Test_shouldExpose(t *testing.T) {
	// exposedByDefault=false: only containers with traefik.enable=true
	strict := createProxy(nil, "", false)
	assert.True(t, strict.shouldExpose(map[string]string{"traefik.enable": "true"}))
	assert.False(t, strict.shouldExpose(map[string]string{}))
	assert.False(t, strict.shouldExpose(map[string]string{"traefik.enable": "false"}))
	assert.False(t, strict.shouldExpose(nil))

	// exposedByDefault=true: everything except traefik.enable=false
	loose := createProxy(nil, "", true)
	assert.True(t, loose.shouldExpose(map[string]string{}))
	assert.True(t, loose.shouldExpose(map[string]string{"traefik.enable": "true"}))
	assert.False(t, loose.shouldExpose(map[string]string{"traefik.enable": "false"}))
}

func Test_proxyHidesUnexposedContainers(t *testing.T) {
	stub := &proxyStubClient{containers: []container.Summary{
		{ID: "enabled", Labels: map[string]string{"traefik.enable": "true"}},
		{ID: "unexposed"},
	}}

	proxyServer := createProxy(stub, "", false)
	papp, proxyDockerEndpoint := proxyServer.start()
	defer papp.Shutdown()

	cli, err := createDockerClient(proxyDockerEndpoint)
	assert.NoError(t, err)

	list, err := cli.ContainerList(context.Background(), container.ListOptions{})
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "enabled", list[0].ID)
}

// the proxy must forward the "all" flag so the docker provider still sees
// stopped containers exactly as it would over a direct connection
func Test_proxyForwardsAllFlag(t *testing.T) {
	stub := &proxyStubClient{containers: []container.Summary{
		{ID: "enabled", Labels: map[string]string{"traefik.enable": "true"}},
	}}

	proxyServer := createProxy(stub, "", true)
	papp, proxyDockerEndpoint := proxyServer.start()
	defer papp.Shutdown()

	cli, err := createDockerClient(proxyDockerEndpoint)
	assert.NoError(t, err)

	_, err = cli.ContainerList(context.Background(), container.ListOptions{All: true})
	assert.NoError(t, err)
	assert.True(t, stub.lastListAll)
}

// with a label prefix set, a plain traefik.enable label is not enough: the
// container must carry the prefixed label to be exposed under this kop instance
func Test_proxyEnableFilterRespectsPrefix(t *testing.T) {
	stub := &proxyStubClient{containers: []container.Summary{
		{ID: "prefixed", Labels: map[string]string{"foo.traefik.enable": "true"}},
		{ID: "plain", Labels: map[string]string{"traefik.enable": "true"}},
	}}

	proxyServer := createProxy(stub, "foo", false)
	papp, proxyDockerEndpoint := proxyServer.start()
	defer papp.Shutdown()

	cli, err := createDockerClient(proxyDockerEndpoint)
	assert.NoError(t, err)

	list, err := cli.ContainerList(context.Background(), container.ListOptions{})
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "prefixed", list[0].ID)
}

func Test_proxyDropsUnexposedEvents(t *testing.T) {
	stub := &proxyStubClient{eventsCh: make(chan events.Message, 2)}
	stub.eventsCh <- events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionDie,
		Actor:  events.Actor{ID: "unexposed", Attributes: map[string]string{"name": "oneshot-job"}},
	}
	stub.eventsCh <- events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor:  events.Actor{ID: "enabled", Attributes: map[string]string{"traefik.enable": "true"}},
	}

	proxyServer := createProxy(stub, "", false)
	papp, proxyDockerEndpoint := proxyServer.start()
	// the events stream connection never closes on its own, so a graceful
	// Shutdown would block forever
	defer papp.ShutdownWithTimeout(100 * time.Millisecond)

	cli, err := createDockerClient(proxyDockerEndpoint)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgCh, errCh := cli.Events(ctx, events.ListOptions{})
	select {
	case m := <-msgCh:
		// the unexposed event must have been dropped by the proxy
		assert.Equal(t, "enabled", m.Actor.ID)
	case err := <-errCh:
		t.Fatalf("unexpected error: %s", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func Test_dockerProxyServerNoPrefix(t *testing.T) {
	mockDockerEndpoint := dockerEndpoint
	mockDockerClient := dc

	// now create our proxy pointing to the mock
	proxyServer := createProxy(mockDockerClient, "", true)
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
	assert.NotEmpty(t, g(store, fmt.Sprintf("traefik/http/routers/%s/service", "hello-detect2")))
	assert.Empty(t, g(store, fmt.Sprintf("traefik/http/routers/%s/service", "prefixed")))
}

func Test_dockerProxyServerPrefix(t *testing.T) {
	mockDockerEndpoint := dockerEndpoint
	mockDockerClient := dc

	// now create our proxy pointing to the mock
	proxyServer := createProxy(mockDockerClient, "foo", true)
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

	assert.Empty(t, g(store, fmt.Sprintf("traefik/http/routers/%s/service", "hello-detect2")))
	assert.NotEmpty(t, g(store, fmt.Sprintf("traefik/http/routers/%s/service", "prefixed")))
}

// test that services with kop.bind.ip or foo.kop.bind.ip labels are handled correctly
func Test_dockerProxyServerPrefixWithKopBindIP(t *testing.T) {
	mockDockerEndpoint := dockerEndpoint
	mockDockerClient := dc

	// now create our proxy pointing to the mock
	proxyServer := createProxy(mockDockerClient, "foo", true)
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
	processFileWithConfig(t, store, nil, "prefix-bindip.yml")

	assertServiceIPs(t, store, []svc{
		{"prefixed", "http", "http://foo.bar.baz:5588"},
		{"unprefixed", "http", "http://example.local:5599"},
	})

	assert.Empty(t, g(store, fmt.Sprintf("traefik/http/routers/%s/service", "hello-detect2")))
	assert.NotEmpty(t, g(store, fmt.Sprintf("traefik/http/routers/%s/service", "prefixed")))
}
