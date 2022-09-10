FROM golang

COPY * /go/
RUN go build -o helloworld ./

ENTRYPOINT ["./helloworld"]
