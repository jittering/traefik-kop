FROM --platform=$BUILDPLATFORM alpine:3.23 AS certificates
RUN apk add --no-cache ca-certificates

FROM scratch
ENTRYPOINT ["/traefik-kop"]
ARG TARGETPLATFORM
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY $TARGETPLATFORM/traefik-kop /traefik-kop
