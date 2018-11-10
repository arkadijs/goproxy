### TCP and UDP proxy in Go

UDP proxy is unidirectional.
Usage:

    $ goproxy [flags] [listen-ip]:port [connect-to-ip]:port
      -debug=false: Print every connection information
      -dns="": DNS server address, supply host:port; will use system default if not set
      -dns-interval=20s: Time interval between DNS queries
      -timeout=10s: TCP connect timeout
      -udp=false: UDP mode
      -verbose=false: Print noticeable info

Via Docker:

    $ docker run --name proxy --restart unless-stopped -d \
        -p 443:443/tcp arkadi/goproxy :443 10.10.20.55:4443

Build Docker image:

    $ docker build . -t arkadi/goproxy

Build static 64-bit Linux binary:

    $ GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
        go build -ldflags '-w -extldflags -static'

Viva [go-nuts](https://groups.google.com/forum/#!topic/golang-nuts/zzW0GL4AP3k)!
