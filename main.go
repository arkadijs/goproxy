package main

import (
	"flag"
	"fmt"
	"github.com/miekg/dns"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	flags       = flag.NewFlagSet("goproxy", flag.ExitOnError)
	dnsAddress  string
	dnsInterval time.Duration
	timeout     time.Duration
	dnsClient   *dns.Client
	connectTo   atomic.Value
	verbose     bool
	debug       bool
)

func main() {
	parseFlags()
	if len(flags.Args()) != 2 {
		if debug {
			log.Printf("Remaining arguments after parsing flags: %+v\n", flags.Args())
		}
		usage()
		os.Exit(1)
	}

	to := flags.Arg(1)
	if verbose {
		log.Printf("Will connect to `%s`\n", to)
	}
	if dnsAddress != "" {
		if verbose {
			log.Printf("DNS server provided: `%s`, will refresh every %v\n", dnsAddress, dnsInterval)
		}
		go refreshDns(to)
	} else {
		connectTo.Store(&to)
	}

	on := flags.Arg(0)
	if verbose {
		log.Printf("Will listen on `%s`\n", on)
	}
	listener, err := net.Listen("tcp", on)
	if err != nil {
		log.Fatalf("Failed to setup TCP listener on `%s`: %v\n", on, err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGPIPE)
	go func() {
		for range c {
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
		} else {
			go forward(conn)
		}
	}
}

func usage() {
	fmt.Fprintf(os.Stderr,
		`Usage: %s [listen-ip]:port [connect-to-ip]:port
Flags:
`, os.Args[0])
	flags.PrintDefaults()
}

func parseFlags() {
	flags.StringVar(&dnsAddress, "dns", "", "DNS server address, supply host:port; will use system default if not set")
	flags.DurationVar(&dnsInterval, "dns-interval", 20*time.Second, "Time interval between DNS queries")
	flags.DurationVar(&timeout, "timeout", 10*time.Second, "TCP connect timeout")
	flags.BoolVar(&verbose, "verbose", false, "Print noticable info")
	flags.BoolVar(&debug, "debug", false, "Print every connection information")
	flags.Usage = usage
	flags.Parse(os.Args[1:])
	if debug {
		verbose = true
	}
}

// https://github.com/benschw/dns-clb-go/blob/master/dns/lib.go
func refreshDns(_connectTo string) {
	host, port, err := net.SplitHostPort(_connectTo)
	if err != nil {
		log.Fatalf("Error parsing `%s`: %v\n", _connectTo, err)
	}
	if host == "" {
		if verbose {
			log.Printf("Only port is provided in `%s`, DNS server address is unused\n", _connectTo)
		}
		connectTo.Store(&_connectTo)
		return
	}
	dnsClient = &dns.Client{}
	name := dns.Fqdn(host)
	qType := dns.StringToType["A"]

	queryDns := func() {
		req := &dns.Msg{}
		req.SetQuestion(name, qType)
		if debug {
			log.Printf("Querying DNS for `%s`\n", name)
		}
		resp, _, err := dnsClient.Exchange(req, dnsAddress)
		if err != nil {
			log.Printf("Error resolving `%s`: %v\n", name, err)
			return
		}
		if req.Id != resp.Id {
			log.Printf("DNS ID mismatch, request: %d, response: %d\n", req.Id, resp.Id)
			return
		}
		to := ""
		for _, r := range resp.Answer {
			if a, ok := r.(*dns.A); ok {
				to = fmt.Sprintf("%s:%s", a.A.String(), port)
				if debug {
					log.Printf("Resolved to `%v`\n", to)
				}
				break
			}
		}
		if to == "" {
			if verbose {
				log.Printf("DNS response has no A record: %+v\n", resp)
			}
		} else {
			if verbose {
				_old := connectTo.Load()
				var old string
				if _old == nil {
					old = "(none)"
				} else {
					old = *_old.(*string)
				}
				if old != to {
					log.Printf("Connect target changed `%s` -> `%s`\n", old, to)
				}
			}
			connectTo.Store(&to)
		}
	}

	queryDns()
	ticker := time.NewTicker(dnsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			queryDns()
		}
	}
}

func forward(conn net.Conn) {
	if debug {
		log.Print("Accepted connection\n")
	}
	_to := connectTo.Load()
	if _to == nil {
		if debug {
			log.Print("Don't know where to connect, closing incoming connection\n")
		}
		conn.Close()
		return
	}
	to := *_to.(*string)
	fwd, err := net.DialTimeout("tcp", to, timeout)
	if err != nil {
		log.Printf("Conection to `%s` failed: %v\n", to, err)
		conn.Close()
		return
	}
	close := func() {
		fwd.Close()
		conn.Close()
	}
	go func() {
		defer close()
		io.Copy(fwd, conn)
		if debug {
			log.Print("Incoming connection closed\n")
		}
	}()
	go func() {
		defer close()
		io.Copy(conn, fwd)
		if debug {
			log.Print("Outgoing connection closed\n")
		}
	}()
}
