package traefikkop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/rs/zerolog/log"
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

// DockerProxyServer is a proxy server that filters docker labels based on a
// prefix and hides containers traefik-kop would not expose
type DockerProxyServer struct {
	upstream         client.APIClient
	labelPrefix      string
	exposedByDefault bool
}

func createProxy(upstream client.APIClient, labelPrefix string, exposedByDefault bool) *DockerProxyServer {
	if labelPrefix != "" && !strings.HasSuffix(labelPrefix, ".") {
		labelPrefix += "."
	}
	return &DockerProxyServer{
		upstream:         upstream,
		labelPrefix:      labelPrefix,
		exposedByDefault: exposedByDefault,
	}
}

// shouldExpose reports whether traefik-kop would expose a container with the
// given (prefix-stripped) labels, mirroring traefik's exposedByDefault and
// traefik.enable handling.
func (s *DockerProxyServer) shouldExpose(labels map[string]string) bool {
	enabled := s.exposedByDefault
	if v, ok := labels["traefik.enable"]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			enabled = b
		}
	}
	return enabled
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
	containers, err := s.upstream.ContainerList(context.Background(), container.ListOptions{All: c.QueryBool("all")})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	filtered := make([]container.Summary, 0, len(containers))
	for i := range containers {
		labels := s.filterLabels(containers[i].Labels)
		if !s.shouldExpose(labels) {
			continue
		}
		containers[i].Labels = labels
		filtered = append(filtered, containers[i])
	}

	return c.JSON(filtered)
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
				if event.Type == "container" {
					event.Actor.Attributes = s.filterLabels(event.Actor.Attributes)
					// drop events from unexposed containers so the provider never re-lists them
					if !s.shouldExpose(event.Actor.Attributes) {
						continue
					}
				}

				err := encoder.Encode(event)
				if err != nil {
					log.Error().Msgf("Error encoding event: %v", err)
				}
			case err := <-errCh:
				if err != nil {
					e := encoder.Encode(err)
					if e != nil {
						log.Error().Msgf("Error encoding error: %v", e)
					}
					return
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
	log.Warn().Msgf("Unhandled request: %s %s", c.Method(), c.OriginalURL())
	return c.Status(fiber.StatusNotFound).SendString("Not Found")
}

func (s *DockerProxyServer) start() (*fiber.App, string) {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	if os.Getenv("DEBUG") != "" {
		app.Use(logger.New())
	}

	app.All("/_ping", func(ctx *fiber.Ctx) error { return ctx.SendStatus(fiber.StatusNoContent) })
	app.Get("/v*/version", s.handleVersion)
	app.Get("/v*/containers/json", s.handleContainersList)
	app.Get("/v*/containers/:id/json", s.handleContainerInspect)
	app.Get("/v*/events", s.handleEvents)
	app.All("/*", s.handleNotFound)

	listener, err := getAvailablePort()
	if err != nil {
		log.Fatal().Err(err)
	}

	go app.Listener(listener)

	dockerEndpoint := fmt.Sprintf("http://localhost:%d", listener.Addr().(*net.TCPAddr).Port)
	log.Debug().Msgf("Started docker proxy at %s with label prefix '%s'", dockerEndpoint, s.labelPrefix)

	return app, dockerEndpoint
}
