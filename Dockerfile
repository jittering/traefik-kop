FROM scratch
ENTRYPOINT ["/traefik-kop"]
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/traefik-kop /traefik-kop
