package traefikkop

import (
	"testing"

	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

func TestConfigReapplier(t *testing.T) {
	var got []string
	r := newConfigReapplier(func(conf dynamic.Configuration) {
		svc := conf.HTTP.Services["whoami"]
		got = append(got, svc.LoadBalancer.Servers[0].URL)
		// the real handler rewrites the URL in place (replaceIPs)
		svc.LoadBalancer.Servers[0].URL = "http://192.168.0.14:32789"
	})

	r.Reapply() // nothing received yet: no call
	if len(got) != 0 {
		t.Fatalf("expected no call before the first configuration, got %v", got)
	}

	conf := dynamic.Configuration{HTTP: &dynamic.HTTPConfiguration{
		Services: map[string]*dynamic.Service{
			"whoami": {LoadBalancer: &dynamic.ServersLoadBalancer{
				Servers: []dynamic.Server{{URL: "http://172.17.0.2:80"}},
			}},
		},
	}}
	r.Handle(conf)
	r.Reapply()
	r.Reapply()

	want := []string{"http://172.17.0.2:80", "http://172.17.0.2:80", "http://172.17.0.2:80"}
	if len(got) != len(want) {
		t.Fatalf("expected %d calls, got %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d: handler must get the original container URL, got %q", i, got[i])
		}
	}
}
