
PROJECT=ghcr.io/jittering/traefik-kop

.DEFAULT_GOAL := run

SHELL := bash

build-docker:
	docker buildx build --platform linux/amd64,linux/arm64 --pull --push -t ${PROJECT}:latest .

build-docker-local:
	if [[ -n "$$PROC" ]]; then \
		docker build --build-arg PROC=$$PROC --pull -t ${PROJECT}:latest .; \
	else \
		docker build --pull -t ${PROJECT}:latest .; \
	fi;

build:
	go build ./bin/traefik-kop

run:
	go run ./bin/traefik-kop

serve: run
