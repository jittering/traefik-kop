package main

import (
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

	traefikkop.Start()

}
