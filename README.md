# traefik-kop

A dynamic docker->redis->traefik discovery agent.

Solves the problem of running a non-Swarm/Kubernetes multi-host cluster with a
single public-facing traefik instance.

```text
                        +---------------------+          +---------------------+
                        |                     |          |                     |
+---------+     :443    |  +---------+        |   :8088  |  +------------+     |
|   WAN   |--------------->| traefik |<-------------------->| svc-nginx  |     |
+---------+             |  +---------+        |          |  +------------+     |
                        |       |             |          |                     |
                        |  +---------+        |          |  +-------------+    |
                        |  |  redis  |<-------------------->| traefik-kop |    |
                        |  +---------+        |          |  +-------------+    |
                        |             docker1 |          |             docker2 |
                        +---------------------+          +---------------------+
```

`traefik-kop` solves this problem by using the same `traefik` docker-provider
logic. It reads the container labels from the local docker node and publishes
them to a given `redis` instance. Simply configure your `traefik` node with a
`redis` provider and point it to the same instance, as in the diagram above.

## Usage

Configure `traefik` to use the redis provider, for example via `traefik.yml`:

```yaml
providers:
  providersThrottleDuration: 2s
  docker:
    watch: true
    endpoint: unix:///var/run/docker.sock
    swarmModeRefreshSeconds: 15s
    exposedByDefault: false
  redis:
    endpoints:
      # assumes a redis link with this service name running on the same
      # docker host as traefik
      - "redis:6379"
```

Run `traefik-kop` on your other nodes via docker-compose:

```yaml
services:
  traefik-kop:
    image: "ghcr.io/jittering/traefik-kop:latest"
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - "REDIS_ADDR=192.168.1.50:6379"
      - "BIND_IP=192.168.1.75"
```

Then add the usual labels to your target service:

```yml
services:
  nginx:
    image: "nginx:alpine"
    restart: unless-stopped
    ports:
      # The host port binding will automatically be picked up for use as the
      # service endpoint. See 'service port binding' in the configuration
      # section for more.
      - 8088:80
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.nginx.rule=Host(`nginx-on-docker2.example.com`)"
      - "traefik.http.routers.nginx.tls=true"
      - "traefik.http.routers.nginx.tls.certresolver=default"
      # [opptional] explicitly set the port binding for this service.
      # See 'service port binding' in the configuration section for more.
      - "traefik.http.services.nginx.loadbalancer.server.scheme=http"
      - "traefik.http.services.nginx.loadbalancer.server.port=8088"
```

See also [bind-ip](#bind-ip) section below.

## Configuration

traefik-kop can be configured via either CLI flags are environment variables.

```text
USAGE:
   traefik-kop [global options] command [command options] [arguments...]

GLOBAL OPTIONS:
   --hostname value       Hostname to identify this node in redis (default: "server.local") [$KOP_HOSTNAME]
   --bind-ip value        IP address to bind services to (default: "auto.detected.ip.addr") [$BIND_IP]
   --redis-addr value     Redis address (default: "127.0.0.1:6379") [$REDIS_ADDR]
   --redis-pass value     Redis password (if needed) [$REDIS_PASS]
   --redis-db value       Redis DB number (default: 0) [$REDIS_DB]
   --redis-ttl value      Redis TTL (in seconds) (default: 0) [$REDIS_TTL]
   --docker-host value    Docker endpoint (default: "unix:///var/run/docker.sock") [$DOCKER_HOST]
   --docker-config value  Docker provider config (file must end in .yaml) [$DOCKER_CONFIG]
   --poll-interval value  Poll interval for refreshing container list (default: 60) [$KOP_POLL_INTERVAL]
   --namespace value      Namespace to process containers for [$NAMESPACE]
   --verbose              Enable debug logging (default: false) [$VERBOSE, $DEBUG]
   --help, -h             show help
   --version, -V          Print the version (default: false)
```

Most important are the `bind-ip` and `redis-addr` flags.

## IP Binding

There are a number of ways to set the IP published to traefik. Below is the
order of precedence (highest first) and detailed descriptions of each setting.

1. `kop.<service name>.bind.ip` label
2. `kop.bind.ip` label
3. Container networking IP
4. `--bind-ip` CLI flag
5. `BIND_IP` env var
6. Auto-detected host IP

### bind-ip

Since your upstream docker nodes are external to your primary traefik server,
traefik needs to connect to these services via the server's public IP rather
than the usual method of using the internal docker-network IPs (by default
172.20.0.x or similar).

When using host networking this can be auto-detected, however it is advisable in
the majority of cases to manually set this to the desired IP address. This can
be done using the docker image by exporting the `BIND_IP` environment variable.

### traefik-kop service labels

The bind IP can be set via label for each service/container.

Labels can be one of two keys:

- `kop.<service name>.bind.ip=2.2.2.2`
- `kop.bind.ip=2.2.2.2`

For a container with a single exposed service, or where all services use
the same IP, the latter is sufficient.

### Load Balancer Merging

If your service is running on multiple nodes and load balanced by traefik, you can enable
merging of load balancers by adding the following label to your container:

- `kop.merge-lbs=true`

When set, kop will check in redis for an existing definition and, if found, append it's service
address to the ones already present.

This setting is off by default as there are some cases where it could cause an issue, such as if
your node's IP changes. In this case, the dead IP would be left in place and the new IP would get
added to the list, causing some of your traffic to fail.

### Container Networking

If your container is configured to use a network-routable IP address via an
overlay network or CNI plugin, that address will override the `bind-ip`
configuration above when the `traefik.docker.network` label is present on the
service.

## Service port binding

By default, the service port will be picked up from the container port bindings
if only a single port is bound. For example:

```yml
services:
  nginx:
    image: "nginx:alpine"
    restart: unless-stopped
    ports:
      - 8088:80
```

`8088` would automatically be used as the service endpoint's port in traefik. If
you have more than one port or are using *host networking*, you will need to
explicitly set the port binding via service label, like so:

```yaml
services:
  nginx:
    image: "nginx:alpine"
    network_mode: host
    ports:
      - 8088:80
      - 8888:81
    labels:
      # (note: other labels snipped for brevity)
      - "traefik.http.services.nginx.loadbalancer.server.port=8088"
```

__NOTE:__ unlike the standard traefik-docker usage, we need to expose the
service port on the host and tell traefik to bind to *that* port (8088 in the
example above) in the load balancer config, not the internal port (80). This is
so that traefik can reach it over the network.

## Namespaces

traefik-kop has the ability to target containers via namespaces. Simply
configure `kop` with a namespace:

```yaml
services:
  traefik-kop:
    image: "ghcr.io/jittering/traefik-kop:latest"
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - "REDIS_ADDR=192.168.1.50:6379"
      - "BIND_IP=192.168.1.75"
      - "NAMESPACE=staging"
```

Then add the `kop.namespace` label to your target services, along with the usual traefik labels:

```yaml
services:
  nginx:
    image: "nginx:alpine"
    restart: unless-stopped
    ports:
      - 8088:80
    labels:
      - "kop.namespace=staging"
      - "traefik.enable=true"
      - "traefik..."
```


## Docker API

traefik-kop expects to connect to the Docker host API via a unix socket, by
default at `/var/run/docker.sock`. The location can be overridden via the
`DOCKER_HOST` env var or `--docker-host` flag.

Other connection methods (like ssh, http/s) are not supported.

By default, `traefik-kop` will listen for push events via the Docker API in
order to detect configuration changes. In some circumstances, a change may not
be pushed correctly. For example, when using healthchecks in certain
configurations, the `start -> healthy` change may not be detected via push
event. As a failsafe, there is an additional polling mechanism to detect those
missed changes.

The default interval of 60 seconds should be light so as not to cause any
issues, however it can be adjusted as needed via the `KOP_POLL_INTERVAL` env var
or set to 0 to disable it completely.

### Traefik Docker Provider Config

In addition to the simple `--docker-host` setting above, all [Docker Provider
configuration
options](https://doc.traefik.io/traefik/providers/docker/#provider-configuration)
are available via the `--docker-config <filename.yaml>` flag which expects
either a filename to read configuration from or an inline YAML document.

For example:

```yaml
services:
  traefik-kop:
    image: "ghcr.io/jittering/traefik-kop:latest"
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      REDIS_ADDR: "172.28.183.97:6380"
      BIND_IP: "172.28.183.97"
      DOCKER_CONFIG: |
        ---
        docker:
          defaultRule: Host(`{{.Name}}.foo.example.com`)
```

## Releasing

To release a new version, simply push a new tag to github.

```sh
git push
git tag -a v0.11.0
git push --tags
```

To update the changelog:

```sh
make update-changelog
# or (replace tag below)
docker run -it --rm -v "$(pwd)":/usr/local/src/your-app \
  githubchangeloggenerator/github-changelog-generator \
  -u jittering -p traefik-kop --output "" \
  --since-tag v0.10.1
```

## License

traefik-kop: MIT, (c) 2015, Pixelcop Research, Inc.

traefik: MIT, (c) 2016-2025 Containous SAS; 2020-2022 Traefik Labs
