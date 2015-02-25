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
	"strings"
	"syscall"
	"time"
)

var (
	flags       = flag.NewFlagSet("goproxy", flag.ExitOnError)
	udp         bool
	dnsAddress  string
	dnsInterval time.Duration
	timeout     time.Duration
	verbose     bool
	debug       bool
)

const none = "(none)"

func main() {
	parseFlags()
	if len(flags.Args()) != 2 {
		if debug {
			log.Printf("Remaining arguments after parsing flags: %+v\n", flags.Args())
		}
		usage()
		os.Exit(1)
	}

	// ignore HUP and PIPE signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGPIPE)
	go func() {
		for range c {
		}
	}()

	// channels to pass DNS updates and new incoming connections
	resolver := make(chan string, 1)
	manager := make(chan net.Conn, 10)

	to := flags.Arg(1)
	if verbose {
		log.Printf("Will connect to `%s`\n", to)
	}
	if dnsAddress != "" {
		if verbose {
			log.Printf("DNS server provided: `%s`, will refresh every %v\n", dnsAddress, dnsInterval)
		}
		go refreshDns(to, resolver)
	} else {
		resolver <- to
	}

	on := flags.Arg(0)
	if verbose {
		proto := "tcp"
		if udp {
			proto = "udp"
		}
		log.Printf("Will listen on `%s://%s`\n", proto, on)
	}

	if udp {
		laddr, err := net.ResolveUDPAddr("udp", on)
		if err != nil {
			log.Fatalf("Error resolving `%s`: %v\n", on, err)
		}
		conn, err := net.ListenUDP("udp", laddr)
		if err != nil {
			log.Fatalf("Failed to setup UDP listener on `%s`: %v\n", on, err)
		}
		manager <- conn
		manageUdp(resolver, manager)
	} else {
		listener, err := net.Listen("tcp", on)
		if err != nil {
			log.Fatalf("Failed to setup TCP listener on `%s`: %v\n", on, err)
		}
		go manageTcp(resolver, manager)
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Failed to accept connection: %v\n", err)
			} else {
				manager <- conn
			}
		}
	}
}

func usage() {
	fmt.Fprintf(os.Stderr,
		`Usage: %s [flags] [listen-ip]:port [connect-to-ip]:port
Flags:
`, os.Args[0])
	flags.PrintDefaults()
}

func parseFlags() {
	flags.BoolVar(&udp, "udp", false, "UDP mode")
	flags.StringVar(&dnsAddress, "dns", "", "DNS server address, supply host:port; will use system default if not set")
	flags.DurationVar(&dnsInterval, "dns-interval", 20*time.Second, "Time interval between DNS queries")
	flags.DurationVar(&timeout, "timeout", 10*time.Second, "TCP connect timeout")
	flags.BoolVar(&verbose, "verbose", false, "Print noticeable info")
	flags.BoolVar(&debug, "debug", false, "Print every connection information")
	flags.Usage = usage
	flags.Parse(os.Args[1:])
	if debug {
		verbose = true
	}
}

func refreshDns(connectTo string, dnsUpdates chan string) {
	host, port, err := net.SplitHostPort(connectTo)
	if err != nil {
		log.Fatalf("Error parsing `%s`: %v\n", connectTo, err)
	}
	if host == "" {
		if verbose {
			log.Printf("Only port is provided in `%s`, DNS server address is unused\n", connectTo)
		}
		dnsUpdates <- connectTo
		return
	}
	// https://github.com/benschw/dns-clb-go/blob/master/dns/lib.go
	dnsClient := &dns.Client{}
	name := dns.Fqdn(host)
	qType := dns.StringToType["A"]
	old := none

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
			to = none
		}
		if old != to {
			dnsUpdates <- to
			if verbose {
				log.Printf("Connect target changed `%s` -> `%s`\n", old, to)
			}
			old = to
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

func manageTcp(resolver chan string, connections chan net.Conn) {
	connectTo := none
	for {
		select {
		case connectTo = <-resolver:

		case in := <-connections:
			if connectTo != none {
				go forwardTcp(in, connectTo)
			} else {
				if debug {
					log.Print("Don't know where to connect, closing incoming connection\n")
				}
				in.Close()
			}
		}
	}
}

func forwardTcp(conn net.Conn, connectTo string) {
	if debug {
		log.Print("Accepted connection\n")
	}
	fwd, err := net.DialTimeout("tcp", connectTo, timeout)
	if err != nil {
		log.Printf("Conection to `%s` failed: %v\n", connectTo, err)
		conn.Close()
		return
	}
	close := func() {
		fwd.Close()
		conn.Close()
	}
	go func() {
		defer close()
		w, err := io.Copy(fwd, conn)
		if debug {
			log.Printf("Incoming TCP connection closed: %v; %v bytes forwarded\n", err, w)
		}
	}()
	go func() {
		defer close()
		w, err := io.Copy(conn, fwd)
		if debug {
			log.Printf("Outgoing TCP connection closed: %v; %v bytes forwarded\n", err, w)
		}
	}()
}

func manageUdp(resolver chan string, connections chan net.Conn) {
	var in *net.Conn
	var out *net.Conn
	for {
		select {
		case connectTo := <-resolver:
			if out != nil {
				(*out).Close()
				out = nil
			}
			if connectTo != none {
				_out, err := net.Dial("udp", connectTo)
				if err != nil {
					log.Printf("Conection to `%s` failed: %v\n", connectTo, err)
				} else {
					out = &_out
					if in != nil {
						go forwardUdp(in, out)
					}
				}
			}

		case _in := <-connections:
			in = &_in
			if out != nil {
				go forwardUdp(in, out)
			}
		}
	}
}

func forwardUdp(from *net.Conn, to *net.Conn) {
	for {
		w, err := io.Copy(*to, *from)
		if debug {
			log.Printf("UDP forwarding interrupted: %v; %v bytes forwarded\n", err, w)
		}
		if strings.Contains(err.Error(), "closed network connection") {
			break
		}
		time.Sleep(1 * time.Second)
	}
}
