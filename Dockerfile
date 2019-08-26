FROM golang:1.12-alpine as builder

RUN apk update && apk upgrade && apk add --no-cache git
WORKDIR /go
RUN go get github.com/miekg/dns
COPY main.go /go/src/goproxy/
RUN CGO_ENABLED=0 go build -ldflags '-w -extldflags -static' goproxy

FROM scratch
COPY --from=builder /go/goproxy /
ENTRYPOINT ["/goproxy"]
