package traefikkop

import (
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DockerConfig string
	DockerHost   string
	Hostname     string
	BindIP       string
	RedisAddr    string
	RedisTTL     int
	RedisUser    string
	RedisPass    string
	RedisDB      int
	PollInterval int64
	Namespace    []string
}

type ConfigFile struct {
	Docker docker.Provider `yaml:"docker"`
}

func loadDockerConfig(input string) (*docker.Provider, error) {
	if input == "" {
		return nil, nil
	}

	var r io.Reader

	if looksLikeFile(input) {
		// see if given filename
		_, err := os.Stat(input)
		if err == nil {
			logrus.Debugf("loading docker config from file %s", input)
			r, err = os.Open(input)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to open docker config %s", input)
			}
		}
	} else {
		logrus.Debugf("loading docker config from yaml input")
		r = strings.NewReader(input) // treat as direct yaml input
	}

	// parse
	conf := ConfigFile{Docker: docker.Provider{}}
	err := yaml.NewDecoder(r).Decode(&conf)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load config")
	}

	return &conf.Docker, nil
}

func looksLikeFile(input string) bool {
	if strings.Contains(input, "\n") {
		return false
	}
	ok, _ := regexp.MatchString(`\.ya?ml`, input)
	return ok
}
