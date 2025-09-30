FROM golang:alpine AS builder

COPY * /go/
RUN go build -o helloworld ./

FROM alpine:latest

COPY --from=builder /go/helloworld /helloworld

ENTRYPOINT ["/helloworld"]
