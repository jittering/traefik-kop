package traefikkop

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"sort"
	"sync"
	"testing"

	"github.com/docker/cli/cli/compose/loader"
	compose "github.com/docker/cli/cli/compose/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/go-connections/nat"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/ryanuber/go-glob"
	"github.com/stretchr/testify/assert"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/safe"
	"github.com/traefik/traefik/v3/pkg/server"
)

type testStore struct {
	kv map[string]interface{}
}

func (s testStore) Ping() error {
	return nil
}

// Add a method to push the last configuration if needed
func (s *testStore) KeepConfAlive() error {
	return nil
}

func (s *testStore) Get(key string) (string, error) {
	if s.kv == nil {
		return "", nil
	}
	val, ok := s.kv[key]
	if !ok {
		return "", nil
	}
	if val == nil {
		return "", nil
	}
	strVal, ok := val.(string)
	if !ok {
		return fmt.Sprintf("%s", val), nil
	}
	return strVal, nil
}

func (s *testStore) Gets(key string) (map[string]string, error) {
	if s.kv == nil {
		return nil, nil
	}
	vals := make(map[string]string)
	for k, _ := range s.kv {
		if glob.Glob(key, k) {
			vals[k], _ = s.Get(k)
		}
	}
	return vals, nil
}

func (s *testStore) Store(conf dynamic.Configuration) error {
	kv, err := ConfigToKV(conf)
	if err != nil {
		return err
	}

	if s.kv == nil {
		s.kv = kv
	} else {
		// merge kv into s.kv
		for k, v := range kv {
			s.kv[k] = v
		}
	}

	return nil
}

type DockerAPIStub struct {
	containers     []container.Summary
	containersJSON map[string]container.InspectResponse
}

func (d DockerAPIStub) ServerVersion(ctx context.Context) (types.Version, error) {
	// Return a version that's compatible with both old and new Docker clients
	return types.Version{
		Version:    "29.0.0",
		APIVersion: "1.45", // Compatible with Docker CE 29.0.0 and API negotiation
	}, nil
}

func (d DockerAPIStub) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	// Implement your logic here
	fmt.Println("Events")
	return nil, nil
}

func (d DockerAPIStub) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	// Implement your logic here
	fmt.Println("ContainerList")
	return d.containers, nil
}

func (d DockerAPIStub) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	// Implement your logic here
	fmt.Println("ContainerInspect", containerID)
	return d.containersJSON[containerID], nil
}

func (d DockerAPIStub) ServiceList(ctx context.Context, options swarm.ServiceListOptions) ([]swarm.Service, error) {
	// Implement your logic here
	fmt.Println("ServiceList")
	return nil, nil
}

func (d DockerAPIStub) NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error) {
	// Implement your logic here
	fmt.Println("NetworkList")
	return nil, nil
}

func createHTTPServer() (*fiber.App, string) {
	app := fiber.New()
	app.Use(logger.New())

	app.Get("/v*/version", func(c *fiber.Ctx) error {
		version, err := dockerAPI.ServerVersion(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(version)
	})

	app.Get("/v*/containers/json", func(c *fiber.Ctx) error {
		containers, err := dockerAPI.ContainerList(c.Context(), container.ListOptions{})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(containers)
	})

	app.Get("/v*/containers/:id/json", func(c *fiber.Ctx) error {
		container, err := dockerAPI.ContainerInspect(c.Context(), c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		// fmt.Printf("returning container: %+v\n", container)
		// print container as json
		// json.NewEncoder((os.Stdout)).Encode(container)
		return c.JSON(container)
	})

	app.Get("/v*/events", func(c *fiber.Ctx) error {
		return nil
	})

	listener, err := getAvailablePort()
	if err != nil {
		log.Fatal(err)
	}

	go app.Listener(listener)

	dockerEndpoint := fmt.Sprintf("http://localhost:%d", listener.Addr().(*net.TCPAddr).Port)

	return app, dockerEndpoint
}

func buildConfigDetails(source map[string]any, env map[string]string) compose.ConfigDetails {
	workingDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	return compose.ConfigDetails{
		WorkingDir: workingDir,
		ConfigFiles: []compose.ConfigFile{
			{Filename: "filename.yml", Config: source},
		},
		Environment: env,
	}
}

func loadYAML(yaml []byte) (*compose.Config, error) {
	return loadYAMLWithEnv(yaml, nil)
}

func loadYAMLWithEnv(yaml []byte, env map[string]string) (*compose.Config, error) {
	dict, err := loader.ParseYAML(yaml)
	if err != nil {
		return nil, err
	}

	return loader.Load(buildConfigDetails(dict, env))
}

// convert compose services to containers
func createContainers(composeConfig *compose.Config) []container.Summary {
	containers := make([]container.Summary, 0)
	for _, service := range composeConfig.Services {
		container := container.Summary{
			ID:     service.Name,
			Labels: service.Labels,
			State:  "running",
			Status: "running",
		}
		// convert ports
		ports := make([]types.Port, 0)
		for _, port := range service.Ports {
			ports = append(ports, types.Port{
				IP:          "172.18.0.2",
				PrivatePort: uint16(port.Target),
				PublicPort:  uint16(port.Published),
				Type:        port.Protocol,
			})
		}
		container.Ports = ports
		containers = append(containers, container)
	}
	return containers
}

// convert compose services to containersJSON
func createContainersJSON(composeConfig *compose.Config) map[string]container.InspectResponse {
	containersJSON := make(map[string]container.InspectResponse)
	for _, service := range composeConfig.Services {
		containerJSON := container.InspectResponse{
			ContainerJSONBase: &types.ContainerJSONBase{
				ID:   service.Name,
				Name: service.Name,
				State: &types.ContainerState{
					Status:  "running",
					Running: true,
				},
				HostConfig: &container.HostConfig{
					NetworkMode:  "testing_default", // network name
					PortBindings: nat.PortMap{},
				},
			},
			Config: &container.Config{
				Labels: service.Labels,
			},
			NetworkSettings: &types.NetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"testing_default": {
						NetworkID: "testing_default", // should normally look like a random id but we can reuse the name here
						IPAddress: "172.18.0.2",
					},
					"foobar": {
						NetworkID: "foobar",
						IPAddress: "10.10.10.5",
					},
				},
			},
		}

		// add port bindings
		for _, port := range service.Ports {
			portID := nat.Port(fmt.Sprintf("%d/%s", port.Published, port.Protocol))
			containerJSON.HostConfig.PortBindings[portID] = []nat.PortBinding{
				{
					HostIP:   "",
					HostPort: fmt.Sprintf("%d", port.Published),
				},
			}
		}
		containerJSON.NetworkSettings.Ports = containerJSON.HostConfig.PortBindings
		for portID, mappings := range containerJSON.NetworkSettings.Ports {
			for i, mapping := range mappings {
				if mapping.HostPort == "0" {
					// Emulating random port assignment for testing
					containerJSON.NetworkSettings.Ports[portID][i].HostPort = "12345"
				}
			}
		}
		containersJSON[service.Name] = containerJSON
	}
	return containersJSON
}

func processFile(t *testing.T, file ...string) TraefikStore {
	store := &testStore{}
	for _, f := range file {
		processFileWithConfig(t, store, nil, f)
	}
	return store
}

func processFileWithConfig(t *testing.T, store TraefikStore, config *Config, file string) TraefikStore {
	p := path.Join("fixtures", file)
	f, err := os.Open(p)
	assert.NoError(t, err)
	if err != nil {
		t.FailNow()
	}

	b, err := io.ReadAll(f)
	assert.NoError(t, err)

	composeConfig, err := loadYAML(b)
	assert.NoError(t, err)

	if store == nil {
		store = &testStore{}
	}

	// fmt.Printf("%+v\n", composeConfig)

	dockerAPI.containers = createContainers(composeConfig)
	dockerAPI.containersJSON = createContainersJSON(composeConfig)

	if config == nil {
		config = &Config{
			BindIP: "192.168.100.100",
		}
	} else {
		config.BindIP = "192.168.100.100"
	}
	if config.DockerHost == "" {
		config.DockerHost = dockerEndpoint
	}

	dp := newDockerProvider(*config)
	handleConfigChange := createConfigHandler(*config, store, dp, dc)

	routinesPool := safe.NewPool(context.Background())
	watcher := server.NewConfigurationWatcher(
		routinesPool,
		dp,
		[]string{},
		"docker",
	)
	watcher.AddListener(handleConfigChange)

	// ensure we get exactly one change
	wgChanges := sync.WaitGroup{}
	wgChanges.Add(1)
	watcher.AddListener(func(c dynamic.Configuration) {
		wgChanges.Done()
	})

	watcher.Start()
	defer watcher.Stop()

	wgChanges.Wait()

	if ts, ok := store.(*testStore); ok {
		// print the kv store with sorted keys
		fmt.Println("printing kv store after processing file:", file)
		keys := make([]string, 0, len(ts.kv))
		for k := range ts.kv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s: %+v\n", k, ts.kv[k])
		}
	}

	return store
}

func assertServiceIP(t *testing.T, store *testStore, serviceName string, ip string) {
	assert.Equal(t, ip, store.kv[fmt.Sprintf("traefik/http/services/%s/loadBalancer/servers/0/url", serviceName)])
}

type svc struct {
	name  string
	proto string
	ip    string
}

func assertServiceIPs(t *testing.T, store TraefikStore, svcs []svc) {
	for _, svc := range svcs {
		path := "url"
		if svc.proto != "http" {
			path = "address"
		}
		key := fmt.Sprintf("traefik/%s/services/%s/loadBalancer/servers/0/%s", svc.proto, svc.name, path)
		val, err := store.Get(key)
		assert.NoError(t, err)
		assert.Equal(t,
			svc.ip,
			val,
			"service has wrong IP at key: %s",
			key,
		)
	}
}
