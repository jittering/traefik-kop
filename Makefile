
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

watch:
	watchexec -e go "make test"

clean:
	rm -rf dist/
	rm -f traefik-kop

release: clean
	goreleaser release --rm-dist --skip-validate

update-changelog:
	echo -e "# Changelog\n" >> temp.md
	rel=$$(gh release list | head -n 1 | awk '{print $$1}'); \
		echo "## $$rel" >> temp.md; \
		echo "" >> temp.md; \
		gh release view --json body $$rel | \
			jq --raw-output '.body' | \
			grep -v '^## Changelog' | \
			sed -e 's/^#/##/g' >> temp.md
	cat CHANGELOG.md | grep -v '^# Changelog' >> temp.md
	mv temp.md CHANGELOG.md
