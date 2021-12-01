package traefikkop

import (
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
	// for k, v := range got {
	// 	fmt.Printf("  %s = %s\n", k, v)
	// }

	require.Contains(t, got, "traefik/http/services/Service0/loadBalancer/healthCheck/port")
	require.Contains(t, got, "traefik/http/middlewares/Middleware15/forwardAuth/authRequestHeaders/1")
	require.NotContains(t, got, "traefik/tls/options/TLS0/sniStrict")
}
