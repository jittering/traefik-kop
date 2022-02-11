#!/bin/bash

# Test a docker run command

docker run --rm -it \
  --label "traefik.enable=true" \
  --label "traefik.http.routers.nginx.rule=Host(\`nginx.local\`)" \
  --label "traefik.http.routers.nginx.tls=true" \
  --label "traefik.http.routers.nginx.tls.certresolver=default" \
  --publish-all \
  nginx:alpine
