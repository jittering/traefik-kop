# Changelog

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