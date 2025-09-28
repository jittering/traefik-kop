package traefikkop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
)

func getAvailablePort() (net.Listener, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// DockerProxyServer is a proxy server that filters docker labels based on a prefix
type DockerProxyServer struct {
	upstream    client.APIClient
	labelPrefix string
}

func createProxy(upstream client.APIClient, labelPrefix string) *DockerProxyServer {
	if labelPrefix != "" && !strings.HasSuffix(labelPrefix, ".") {
		labelPrefix += "."
	}
	return &DockerProxyServer{
		upstream:    upstream,
		labelPrefix: labelPrefix,
	}
}

func (s *DockerProxyServer) filterLabels(labels map[string]string) map[string]string {
	if s.labelPrefix == "" || labels == nil {
		return labels
	}

	newLabels := make(map[string]string)

	for k, v := range labels {
		if strings.HasPrefix(k, s.labelPrefix) {
			// strip prefix from our labels
			newKey := strings.TrimPrefix(k, s.labelPrefix)
			newLabels[newKey] = v
		} else if !strings.HasPrefix(k, "traefik.") {
			// keep every other than traefik.* labels as is
			newLabels[k] = v
		}
	}

	return newLabels
}

func (s *DockerProxyServer) handleVersion(c *fiber.Ctx) error {
	v, err := s.upstream.ServerVersion(context.Background())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(v)
}

func (s *DockerProxyServer) handleContainersList(c *fiber.Ctx) error {
	containers, err := s.upstream.ContainerList(context.Background(), container.ListOptions{})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// modify labels
	for _, container := range containers {
		container.Labels = s.filterLabels(container.Labels)
	}

	return c.JSON(containers)
}

func (s *DockerProxyServer) handleContainerInspect(c *fiber.Ctx) error {
	container, err := s.upstream.ContainerInspect(context.Background(), c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if container.Config != nil {
		container.Config.Labels = s.filterLabels(container.Config.Labels)
	}

	return c.JSON(container)
}

func (s *DockerProxyServer) handleEvents(c *fiber.Ctx) error {
	var fa filters.Args
	f := c.Query("filters")
	if f != "" {
		var err error
		fa, err = filters.FromJSON(f)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}

	eventsCh, errCh := s.upstream.Events(context.Background(), events.ListOptions{Filters: fa})

	c.Status(fiber.StatusOK).Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		encoder := json.NewEncoder(w)
		for {
			select {
			case event := <-eventsCh:
				if event.Type == "container" && event.Actor.Attributes != nil {
					event.Actor.Attributes = s.filterLabels(event.Actor.Attributes)
				}

				err := encoder.Encode(event)
				if err != nil {
					logrus.Errorf("Error encoding event: %v", err)
				}
			case err := <-errCh:
				if err != nil {
					e := encoder.Encode(err)
					if e != nil {
						logrus.Errorf("Error encoding error: %v", e)
					}
				}
			}
			err := w.Flush()
			if err != nil {
				break
			}
		}
	}))

	return nil
}

func (s *DockerProxyServer) handleNotFound(c *fiber.Ctx) error {
	logrus.Warnf("Unhandled request: %s %s", c.Method(), c.OriginalURL())
	return c.Status(fiber.StatusNotFound).SendString("Not Found")
}

func (s *DockerProxyServer) start() (*fiber.App, string) {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	if os.Getenv("DEBUG") != "" {
		app.Use(logger.New())
	}

	app.Get("/v*/version", s.handleVersion)
	app.Get("/v*/containers/json", s.handleContainersList)
	app.Get("/v*/containers/:id/json", s.handleContainerInspect)
	app.Get("/v*/events", s.handleEvents)
	app.Get("/*", s.handleNotFound)

	listener, err := getAvailablePort()
	if err != nil {
		log.Fatal(err)
	}

	go app.Listener(listener)

	dockerEndpoint := fmt.Sprintf("http://localhost:%d", listener.Addr().(*net.TCPAddr).Port)

	return app, dockerEndpoint
}
