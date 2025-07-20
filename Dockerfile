FROM scratch
ENTRYPOINT ["/traefik-kop"]
COPY traefik-kop-linux /traefik-kop
