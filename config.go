package traefikkop

type Config struct {
	DockerHost   string
	Hostname     string
	BindIP       string
	Addr         string
	Pass         string
	DB           int
	PollInterval int64
}
