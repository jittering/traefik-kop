# traefik-kop

A dynamic docker->redis->traefik discovery agent.

Solves the problem of running a non-Swarm/Kubernetes multi-host cluster with a
single public-facing traefik instance. For example:

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

Configure `traefik` to use the redis provider:

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
version: "3"
services:
  nginx:
    image: "ghcr.io/jittering/traefik-kop:latest"
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - "REDIS_ADDR=192.168.1.50:6379"
      - "BIND_IP=192.168.1.75"
```

## Configuration

traefik-kop can be configured via either CLI flags are environment variables.

```text
USAGE:
   traefik-kop [global options] command [command options] [arguments...]

GLOBAL OPTIONS:
   --hostname value    Hostname to identify this node in redis (default: "culture.local") [$KOP_HOSTNAME]
   --bind-ip value     IP address to bind services to (default: "192.168.80.99") [$BIND_IP]
   --redis-addr value  Redis address (default: "127.0.0.1:6379") [$REDIS_ADDR]
   --redis-pass value  Redis password (if needed) [$REDIS_PASS]
   --redis-db value    Redis DB number (default: 0) [$REDIS_DB]
   --verbose           Enable debug logging (default: false) [$VERBOSE, $DEBUG]
   --help, -h          show help (default: false)
```

Most important are the `bind-ip` and `redis-addr` flags.

### bind-ip

Since your upstream docker nodes are external to your primary traefik server,
traefik needs to connect to these services via the server's public IP rather
than the usual method of using the internal docker-network IPs (by default
172.20.0.x or similar).

When using host networking this can be auto-detected, however it is advisable in
the majority of cases to manually set this to the desired IP address. This can
be done using the docker image by exporting the `BIND_IP` environment variable.

## License

traefik-kop: MIT, (c) 2021, Pixelcop Research, Inc.

traefik: MIT, (c) 2016-2020 Containous SAS; 2020-2021 Traefik Labs
