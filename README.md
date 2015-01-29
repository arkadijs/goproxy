### TCP proxy in Go

Usage:

    $ goproxy [listen-ip]:port [connect-to-ip]:port
    Flags:
      -debug=false: Print every connection information
      -dns="": DNS server address, supply host:port; will use system default if not set
      -dns-interval=20s: Time interval between DNS queries
      -timeout=10s: TCP connect timeout
      -verbose=false: Print noticable info

Viva [go-nuts](https://groups.google.com/forum/#!topic/golang-nuts/zzW0GL4AP3k)!
