package main

import (
	"fmt"
	"net"
	"os"

	traefikkop "github.com/jittering/traefik-kop"
	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/log"
	"github.com/urfave/cli/v2"
)

var (
	version string
	commit  string
	date    string
	builtBy string
)

func printVersion(c *cli.Context) error {
	fmt.Printf("%s version %s (commit: %s, built %s)\n", c.App.Name, c.App.Version, commit, date)
	return nil
}

func flags() {
	if version == "" {
		version = "n/a"
	}
	if commit == "" {
		commit = "head"
	}
	if date == "" {
		date = "n/a"
	}

	cli.VersionFlag = &cli.BoolFlag{
		Name:    "version",
		Aliases: []string{"V"},
		Usage:   "Print the version",
	}
	cli.VersionPrinter = func(c *cli.Context) {
		printVersion(c)
	}

	app := &cli.App{
		Name:    "traefik-kop",
		Usage:   "A dynamic docker->redis->traefik discovery agent",
		Version: version,

		Action: doStart,

		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "hostname",
				Usage:   "Hostname to identify this node in redis",
				Value:   getHostname(),
				EnvVars: []string{"KOP_HOSTNAME"},
			},
			&cli.StringFlag{
				Name:    "bind-ip",
				Usage:   "IP address to bind services to",
				Value:   getDefaultIP(),
				EnvVars: []string{"BIND_IP"},
			},
			&cli.StringFlag{
				Name:    "redis-addr",
				Usage:   "Redis address",
				Value:   "127.0.0.1:6379",
				EnvVars: []string{"REDIS_ADDR"},
			},
			&cli.StringFlag{
				Name:    "redis-pass",
				Usage:   "Redis password (if needed)",
				EnvVars: []string{"REDIS_PASS"},
			},
			&cli.IntFlag{
				Name:    "redis-db",
				Usage:   "Redis DB number",
				Value:   0,
				EnvVars: []string{"REDIS_DB"},
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Usage:   "Enable debug logging",
				Value:   false,
				EnvVars: []string{"VERBOSE", "DEBUG"},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func setupLogging(debug bool) {
	if debug {
		logrus.SetLevel(logrus.DebugLevel)
		log.SetLevel(logrus.DebugLevel)
		log.WithoutContext().WriterLevel(logrus.DebugLevel)
	}

	formatter := &logrus.TextFormatter{DisableColors: true, FullTimestamp: true, DisableSorting: true}
	logrus.SetFormatter(formatter)
	log.SetFormatter(formatter)
}

func main() {
	flags()
}

func doStart(c *cli.Context) error {
	traefikkop.Version = version
	config := traefikkop.Config{
		Hostname: c.String("hostname"),
		BindIP:   c.String("bind-ip"),
		Addr:     c.String("redis-addr"),
		Pass:     c.String("redis-pass"),
		DB:       c.Int("redis-db"),
	}

	setupLogging(c.Bool("verbose"))

	traefikkop.Start(config)
	return nil
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return hostname
}

func getDefaultIP() string {
	ip := GetOutboundIP()
	return ip.String()
}

// Get preferred outbound ip of this machine
// via https://stackoverflow.com/a/37382208/102920
func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}
