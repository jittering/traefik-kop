package traefikkop

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

func Test_configToKV(t *testing.T) {
	cfg := &dynamic.Configuration{}
	_, err := toml.DecodeFile("./fixtures/sample.toml", &cfg)
	require.NoError(t, err)

	got, err := ConfigToKV(*cfg)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.Contains(t, got, "traefik/http/services/Service0/loadBalancer/healthCheck/port")
	require.Contains(t, got, "traefik/http/middlewares/Middleware15/forwardAuth/authRequestHeaders/1")
	require.NotContains(t, got, "traefik/tls/options/TLS0/sniStrict")

	// should not include @docker in names
	cfg = &dynamic.Configuration{}
	err = json.Unmarshal([]byte(NGINX_CONF_JSON), cfg)
	require.NoError(t, err)

	got, err = ConfigToKV(*cfg)
	require.NoError(t, err)
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

func Test_stringify(t *testing.T) {
	v := 15552000
	s := stringify(v)
	require.Equal(t, "15552000", s, "int should match")

	b := false
	s = stringify(b)
	require.Equal(t, "false", s, "bool should match")

	b = true
	s = stringify(b)
	require.Equal(t, "true", s, "bool should match")
}

func Test_stringifyFloat(t *testing.T) {
	f := float64(15552000)
	s := stringifyFloat(f)
	require.Equal(t, "15552000", s, "float should match")

	f32 := float32(15552000)
	s = stringifyFloat(float64(f32))
	require.Equal(t, "15552000", s, "float should match")
}
