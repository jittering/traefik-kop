# Changelog

## v0.12

### Notes

By default, `traefik-kop` will listen for push events via the Docker API in
order to detect configuration changes. In some circumstances, a change may not
be pushed correctly. For example, when using healthchecks in certain
configurations, the `start -> healthy` change may not be detected via push
event. As a failsafe, there is an additional polling mechanism to detect those
missed changes.

The default interval of 60 seconds should be light so as not to cause any
issues, however it can be adjusted as needed via the `KOP_POLL_INTERVAL` env var
or set to 0 to disable it completely.

[Full Changelog](https://github.com/jittering/traefik-kop/compare/v0.11...v0.12)

- [347352b](https://github.com/jittering/traefik-kop/commit/347352b) build: fix goreleaser tidy
- [b6447c3](https://github.com/jittering/traefik-kop/commit/b6447c3) build: go mod tidy
- [12ad255](https://github.com/jittering/traefik-kop/commit/12ad255) docs: added poll interval to readme
- [10f7aab](https://github.com/jittering/traefik-kop/commit/10f7aab) feat: expose providers in case anyone wants to reuse
- [5b58547](https://github.com/jittering/traefik-kop/commit/5b58547) feat: add log message when explicitly disabling polling
- [02802d5](https://github.com/jittering/traefik-kop/commit/02802d5) feat: configurable poll interval (default 60)
- [b2ef52b](https://github.com/jittering/traefik-kop/commit/b2ef52b) feat: combine providers into single config watcher
- [07fe8aa](https://github.com/jittering/traefik-kop/commit/07fe8aa) feat: added polling provider as a workaround for healthcheck issue
- [cc3854b](https://github.com/jittering/traefik-kop/commit/cc3854b) feat: added config for changing docker endpoint
- [c309d40](https://github.com/jittering/traefik-kop/commit/c309d40) build: upgraded traefik lib to v2.7
- [32c2df6](https://github.com/jittering/traefik-kop/commit/32c2df6) test: added pihole container (with builtin healthcheck)
- [e770242](https://github.com/jittering/traefik-kop/commit/e770242) docs: updated changelog



## v0.11


[Full Changelog](https://github.com/jittering/traefik-kop/compare/v0.10.1...v0.11)

#### Notes

* If your container is configured to use a network-routable IP address via an
overlay network or CNI plugin, that address will override the `bind-ip`
configuration when the `traefik.docker.network` label is present.

**Merged pull requests:**

- Add support for `traefik.docker.network` [\#8](https://github.com/jittering/traefik-kop/pull/8) ([hcooper](https://github.com/hcooper))


## v0.10.1

* e0af6eb Merge pull request #7 from jittering/fix/port-detect



## v0.10.1

* e0af6eb Merge pull request #7 from jittering/fix/port-detect



## v0.10.0

* 5d029d2 feat: add support for ports published via --publish-all (closes #6)



## v0.9.2

* 5871d16 feat: log the container name/id if found



## v0.9.1


* fbd2d1d fix: Automatic port assignment not working for containers without a service


## v0.9


* 4bd7cd1 Merge pull request #2 from jittering/feature/detect-host-port



## v0.8.1


* e69bd05 fix: strip @docker when removing keys


### Docker images

- `docker pull ghcr.io/jittering/traefik-kop:0.8.1`


## v0.8


* dccbf22 build: fix release step


### Docker images

- `docker pull ghcr.io/jittering/traefik-kop:0.8`
