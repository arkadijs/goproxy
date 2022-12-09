FROM golang:1.19-alpine as builder

RUN apk update && apk upgrade && apk add --no-cache git
WORKDIR /go/src/goproxy
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags '-w -extldflags -static'

FROM scratch
COPY --from=builder /go/src/goproxy/goproxy /
ENTRYPOINT ["/goproxy"]
