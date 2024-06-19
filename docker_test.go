package traefikkop

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/stretchr/testify/assert"
)

type DockerAPIStub struct{}

func (d DockerAPIStub) ServerVersion(ctx context.Context) (types.Version, error) {
	// Implement your logic here
	return types.Version{
		Version: "1.0.0",
	}, nil
}

func (d DockerAPIStub) Events(ctx context.Context, options types.EventsOptions) (<-chan events.Message, <-chan error) {
	// Implement your logic here
	return nil, nil
}

func (d DockerAPIStub) ContainerList(ctx context.Context, options types.ContainerListOptions) ([]types.Container, error) {
	// Implement your logic here
	return nil, nil
}

func (d DockerAPIStub) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	// Implement your logic here
	return types.ContainerJSON{}, nil
}

func (d DockerAPIStub) ServiceList(ctx context.Context, options types.ServiceListOptions) ([]swarm.Service, error) {
	// Implement your logic here
	return nil, nil
}

func (d DockerAPIStub) NetworkList(ctx context.Context, options types.NetworkListOptions) ([]types.NetworkResource, error) {
	// Implement your logic here
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

var app *fiber.App
var port int
var dc client.APIClient

func createHTTPServer() {
	app = fiber.New()
	app.Use(logger.New())

	dockerAPI := DockerAPIStub{}

	app.Get("/v1.24/version", func(c *fiber.Ctx) error {
		version, err := dockerAPI.ServerVersion(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(version)
	})

	var err error
	port, err = getAvailablePort()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Available port:", port)

	go app.Listen(fmt.Sprintf(":%d", port))
}

func setup() {
	createHTTPServer()
	var err error
	dc, err = createDockerClient(fmt.Sprintf("http://localhost:%d", port))
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
