
PROJECT=ghcr.io/jittering/traefik-kop

.DEFAULT_GOAL := run

SHELL := bash

build-docker: build-linux
	docker build --platform linux/amd64 -t ${PROJECT}:latest .

build-linux:
	GOOS=linux go build ./bin/traefik-kop

build:
	go build ./bin/traefik-kop

run:
	go run ./bin/traefik-kop

serve: run

test:
	go test ./...

clean:
	rm -rf dist/
	rm -f traefik-kop

release: clean
	goreleaser release --rm-dist --skip-validate
