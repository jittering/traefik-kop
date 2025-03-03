package traefikkop

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
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
	"github.com/stretchr/testify/assert"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
	"github.com/traefik/traefik/v2/pkg/safe"
	"github.com/traefik/traefik/v2/pkg/server"
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

func (s *testStore) Store(conf dynamic.Configuration) error {
	kv, err := ConfigToKV(conf)
	if err != nil {
		return err
	}
	s.kv = kv
	return nil
}

type DockerAPIStub struct {
	containers     []types.Container
	containersJSON map[string]types.ContainerJSON
}

func (d DockerAPIStub) ServerVersion(ctx context.Context) (types.Version, error) {
	// Implement your logic here
	return types.Version{
		Version:    "1.0.0",
		APIVersion: "1.0.0-test",
	}, nil
}

func (d DockerAPIStub) Events(ctx context.Context, options types.EventsOptions) (<-chan events.Message, <-chan error) {
	// Implement your logic here
	fmt.Println("Events")
	return nil, nil
}

func (d DockerAPIStub) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	// Implement your logic here
	fmt.Println("ContainerList")
	return d.containers, nil
}

func (d DockerAPIStub) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	// Implement your logic here
	fmt.Println("ContainerInspect", containerID)
	return d.containersJSON[containerID], nil
}

func (d DockerAPIStub) ServiceList(ctx context.Context, options types.ServiceListOptions) ([]swarm.Service, error) {
	// Implement your logic here
	fmt.Println("ServiceList")
	return nil, nil
}

func (d DockerAPIStub) NetworkList(ctx context.Context, options types.NetworkListOptions) ([]types.NetworkResource, error) {
	// Implement your logic here
	fmt.Println("NetworkList")
	return nil, nil
}

func getAvailablePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func createHTTPServer() (*fiber.App, string) {
	app := fiber.New()
	app.Use(logger.New())

	app.Get("/v1.24/version", func(c *fiber.Ctx) error {
		version, err := dockerAPI.ServerVersion(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(version)
	})

	app.Get("/v1.24/containers/json", func(c *fiber.Ctx) error {
		containers, err := dockerAPI.ContainerList(c.Context(), types.ContainerListOptions{})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(containers)
	})

	app.Get("/v1.24/containers/:id/json", func(c *fiber.Ctx) error {
		container, err := dockerAPI.ContainerInspect(c.Context(), c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		// fmt.Printf("returning container: %+v\n", container)
		// print container as json
		// json.NewEncoder((os.Stdout)).Encode(container)
		return c.JSON(container)
	})

	port, err := getAvailablePort()
	if err != nil {
		log.Fatal(err)
	}
	// log.Println("Available port:", port)

	go app.Listen(fmt.Sprintf(":%d", port))

	dockerEndpoint := fmt.Sprintf("http://localhost:%d", port)

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
func createContainers(composeConfig *compose.Config) []types.Container {
	containers := make([]types.Container, 0)
	for _, service := range composeConfig.Services {
		container := types.Container{
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
func createContainersJSON(composeConfig *compose.Config) map[string]types.ContainerJSON {
	containersJSON := make(map[string]types.ContainerJSON)
	for _, service := range composeConfig.Services {
		containerJSON := types.ContainerJSON{
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
				NetworkSettingsBase: types.NetworkSettingsBase{},
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
			containerJSON.NetworkSettings.Ports = containerJSON.HostConfig.PortBindings
		}
		containersJSON[service.Name] = containerJSON
	}
	return containersJSON
}

func doTest(t *testing.T, file string, config *Config) *testStore {
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

	store := &testStore{}

	// fmt.Printf("%+v\n", composeConfig)

	dockerAPI.containers = createContainers(composeConfig)
	dockerAPI.containersJSON = createContainersJSON(composeConfig)

	dp := &docker.Provider{}
	dp.Watch = false
	dp.Endpoint = dockerEndpoint

	if config == nil {
		config = &Config{
			BindIP: "192.168.100.100",
		}
	} else {
		config.BindIP = "192.168.100.100"
	}

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

	// print the kv store
	for k, v := range store.kv {
		fmt.Printf("%s: %+v\n", k, v)
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

func assertServiceIPs(t *testing.T, store *testStore, svcs []svc) {
	for _, svc := range svcs {
		path := "url"
		if svc.proto != "http" {
			path = "address"
		}
		key := fmt.Sprintf("traefik/%s/services/%s/loadBalancer/servers/0/%s", svc.proto, svc.name, path)
		assert.Equal(t,
			svc.ip,
			store.kv[key],
			"service has wrong IP at key: %s",
			key,
		)
	}
}
