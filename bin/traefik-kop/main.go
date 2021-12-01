package main

import (
	"net"
	"os"

	traefikkop "github.com/jittering/traefik-kop"
	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/log"
)

func main() {

	logrus.SetLevel(logrus.TraceLevel)
	log.SetLevel(logrus.DebugLevel)
	log.WithoutContext().WriterLevel(logrus.DebugLevel)

	formatter := &logrus.TextFormatter{DisableColors: true, FullTimestamp: true, DisableSorting: true}
	logrus.SetFormatter(formatter)
	log.SetFormatter(formatter)

	hostname, _ := os.Hostname()
	config := traefikkop.Config{
		Hostname: hostname,
		BindIP:   getDefaultIP(),
		Addr:     "127.0.0.1:6379",
	}

	traefikkop.Start(config)
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
