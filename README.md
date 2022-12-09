### TCP and UDP proxy in Go

UDP proxy is unidirectional.
Usage:

    $ goproxy [flags] [listen-ip]:port [connect-to-ip]:port
    Flags:
    -debug
            Print debug level info
    -dns string
            DNS server address, supply host[:port]; will use system default if not set
    -dns-interval duration
            Time interval between DNS queries (default 20s)
    -srv
            Query DNS for SRV records, -dns must be specified
    -timeout duration
            TCP connect timeout (default 10s)
    -udp
            UDP mode
    -verbose
            Print noticeable info

Via Docker:

    $ docker run --name proxy --restart unless-stopped -d \
        -p 443:443/tcp arkadi/goproxy :443 10.10.20.55:4443

Build Docker image:

    $ docker build . -t arkadi/goproxy

Build static 64-bit Linux binary:

    $ GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
        go build -ldflags '-w -extldflags -static'

Note, for a multi-value SRV record the target pool could be unstable as DNS server may only return a subset of the target records (eight records on AWS).

Viva [go-nuts](https://groups.google.com/forum/#!topic/golang-nuts/zzW0GL4AP3k)!
