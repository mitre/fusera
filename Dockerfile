FROM golang:1.10

WORKDIR /go/src/github.com/mitre/fusera
COPY . .

RUN apt-get update && apt-get install -y fuse

RUN go get -d -v ./...
RUN go install -v ./...

ENTRYPOINT ["fusera"]
