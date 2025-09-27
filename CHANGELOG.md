# Changelog

## Unreleased

### New Features

- Add `--bind-interface` (env: `BIND_INTERFACE`) to select the network interface from which to derive the bind IP when `--bind-ip` is not set. This requires the container to be run with `network_mode: host`.

## v0.17

### New Features

- Support for setting a TTL on Redis keys [#58](https://github.com/jittering/traefik-kop/pull/58)
- Support for merging load balancers across multiple nodes managed by `traefik-kop` [#59](https://github.com/jittering/traefik-kop/pull/59)

### Fixes

- Allow outbound IP detection to fail [#56](https://github.com/jittering/traefik-kop/pull/56)
- Normalize container labels [#56](https://github.com/jittering/traefik-kop/pull/57)

## v0.16

### Fixes

- Fix port detection when publishing to ephemeral ports [#48, thanks @Flamefork](https://github.com/jittering/traefik-kop/pull/48)

## v0.15

### Fixes

- Push last config to redis in the case of a restart or failure in the cache [#46](https://github.com/jittering/traefik-kop/pull/46)

## v0.14

### New Features

- Allow filtering containers processed by `traefik-kop` using [namespaces](https://github.com/jittering/traefik-kop#namespaces)

### Fixes

- Use exact service name match when searching container labels (#39, thanks @damfleu)

**Full Changelog**: https://github.com/jittering/traefik-kop/compare/v0.13.3...v0.14

## v0.13.3

- 16beda8 build: bump go version to 1.22

## v0.13.2

- 10ab916 fix: properly stringify floats when writing to redis (resolves #25)

## v0.13.1

* [build: upgraded docker client dep](https://github.com/jittering/traefik-kop/commit/e7f30f3108f46cf0d174369b45f59d57398d002b)
* [fix: NPE when creating error message from port map](https://github.com/jittering/traefik-kop/commit/80d40e2aa904a78d4ec7b311c9f99bc449f556f3) ([fixes #24](https://github.com/jittering/traefik-kop/issues/24))
* [fix: avoid possible NPE when resolving CNI container IP](https://github.com/jittering/traefik-kop/commit/37686b0089ccaf91d4fa13df62447e15671944dd)



## [v0.13](https://github.com/jittering/traefik-kop/tree/v0.13) (2022-10-17)

[Full Changelog](https://github.com/jittering/traefik-kop/compare/v0.12.1...v0.13)

### New Features

- Set bind IP per-container or service
- Set traefik docker provider config (e.g., `defaultRule`)

### Fixes

- Correctly set port for TCP and UDP services

### Closed issues

- Go runtime error [\#20](https://github.com/jittering/traefik-kop/issues/20)
- Default Rule [\#18](https://github.com/jittering/traefik-kop/issues/18)
- Provide IP for each docker via label [\#17](https://github.com/jittering/traefik-kop/issues/17)
- setting port for tcp service does not work [\#16](https://github.com/jittering/traefik-kop/issues/16)
- Doesn't work with multiple services on one container [\#14](https://github.com/jittering/traefik-kop/issues/14)

## v0.12.1

This release updates the upstream version of the traefik library to v2.8.4 and
adds additional logging around port detection (both debug and info levels) to
make it easier to see what's going on and troubleshoot various scenarios.

[Full Changelog](https://github.com/jittering/traefik-kop/compare/v0.12...v0.12.1)

- [8c5a3f0](https://github.com/jittering/traefik-kop/commit/8c5a3f0) build: bump actions/cache to v3
- [dad6e90](https://github.com/jittering/traefik-kop/commit/dad6e90) build: bump go version in github actions
- [f009b84](https://github.com/jittering/traefik-kop/commit/f009b84) docs: added more detail and logging around port selection
- [2f18114](https://github.com/jittering/traefik-kop/commit/2f18114) test: added helloworld service for testing multiple bindings
- [be636f7](https://github.com/jittering/traefik-kop/commit/be636f7) build: upgraded traefik to 2.8.4 (now supports go 1.18+)

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
