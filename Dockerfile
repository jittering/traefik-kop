# BUILDER image
FROM golang:alpine AS builder

ARG PROC=1

RUN mkdir /app
COPY go.* /app/
WORKDIR /app
RUN go mod download

COPY . /app
RUN go build -p 1 ./bin/traefik-kop

# RUNTIME image
FROM alpine AS runtime

RUN mkdir /app

COPY --from=builder /app/traefik-kop /app/

WORKDIR /app
ENTRYPOINT ["/app/traefik-kop"]
