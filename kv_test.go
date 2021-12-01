package traefikkop

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
)

func Test_configToKV(t *testing.T) {
	cfg := &dynamic.Configuration{}
	_, err := toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)

	got := ConfigToKV(*cfg)
	require.NotNil(t, got)

	require.Contains(t, got, "traefik/http/services/Service0/loadBalancer/healthCheck/port")
	require.Contains(t, got, "traefik/http/middlewares/Middleware15/forwardAuth/authRequestHeaders/1")
	require.NotContains(t, got, "traefik/tls/options/TLS0/sniStrict")

	// should not include @docker in names
	cfg = &dynamic.Configuration{}
	err = json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)

	got = ConfigToKV(*cfg)
	// dumpKV(got)
	require.NotContains(t, got, "traefik/http/routers/nginx@docker/service")
	require.NotContains(t, got, "traefik/http/services/nginx@docker/loadBalancer/passHostHeader")

	// t.Fail()
}

func dumpKV(kv map[string]interface{}) {
	for k, v := range kv {
		fmt.Printf("%s = %s\n", k, v)
	}
}
