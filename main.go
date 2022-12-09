package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

var (
	flags       = flag.NewFlagSet("goproxy", flag.ExitOnError)
	udp         bool
	srv         bool
	dnsServer   string
	dnsInterval time.Duration
	timeout     time.Duration
	verbose     bool
	debug       bool
)

func main() {
	parseFlags()
	if len(flags.Args()) < 2 {
		if debug {
			log.Printf("Remaining arguments after parsing flags: %+v\n", flags.Args())
		}
		usage()
		os.Exit(1)
	}

	if dnsServer != "" && !strings.Contains(dnsServer, ":") && !strings.Contains(dnsServer, "/") {
		dnsServer = net.JoinHostPort(dnsServer, "53")
	}

	// ignore HUP and PIPE signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGPIPE)
	go func() {
		for range c {
		}
	}()

	// channels to pass DNS updates and new incoming connections
	resolver := make(chan []string, 1)
	manager := make(chan net.Conn, 10)

	connectTo := flags.Args()[1:]
	if verbose {
		log.Printf("Will connect to %v\n", connectTo)
	}
	if dnsServer != "" {
		if verbose {
			log.Printf("DNS server provided: `%s`, will refresh every %v\n", dnsServer, dnsInterval)
		}
		go refreshDns(connectTo, resolver)
	} else {
		resolver <- connectTo
	}

	listenOn := flags.Arg(0)
	if verbose {
		proto := "tcp"
		if udp {
			proto = "udp"
		}
		log.Printf("Will listen on `%s://%s`\n", proto, listenOn)
	}

	if udp {
		laddr, err := net.ResolveUDPAddr("udp", listenOn)
		if err != nil {
			log.Fatalf("Error resolving `%s`: %v\n", listenOn, err)
		}
		conn, err := net.ListenUDP("udp", laddr)
		if err != nil {
			log.Fatalf("Failed to setup UDP listener on `%s`: %v\n", listenOn, err)
		}
		manager <- conn
		manageUdp(resolver, manager)
	} else {
		listener, err := net.Listen("tcp", listenOn)
		if err != nil {
			log.Fatalf("Failed to setup TCP listener on `%s`: %v\n", listenOn, err)
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
	flags.BoolVar(&srv, "srv", false, "Query DNS for SRV records, -dns must be specified")
	flags.StringVar(&dnsServer, "dns", "", "DNS server address, supply host[:port]; will use system default if not set")
	flags.DurationVar(&dnsInterval, "dns-interval", 20*time.Second, "Time interval between DNS queries")
	flags.DurationVar(&timeout, "timeout", 10*time.Second, "TCP connect timeout")
	flags.BoolVar(&verbose, "verbose", false, "Print noticeable info")
	flags.BoolVar(&debug, "debug", false, "Print debug level info")
	flags.Usage = usage
	flags.Parse(os.Args[1:])
	if debug {
		verbose = true
	}
}

type HostPort struct {
	host, port string
	resolve    bool
}

func queryDns(dnsClient *dns.Client, name string, qType uint16) []HostPort {
	if qType != dns.TypeA && qType != dns.TypeSRV {
		log.Fatalf("Unsupported DNS query type `%s` resolving `%s`", dns.TypeToString[qType], name)
	}

	req := &dns.Msg{}
	req.SetQuestion(name, qType)
	if debug {
		log.Printf("Querying DNS for `%s` type %s\n", name, dns.TypeToString[qType])
	}

	resp, _, err := dnsClient.Exchange(req, dnsServer)
	if err != nil {
		log.Printf("Error resolving `%s`: %v\n", name, err)
		return nil
	}
	if req.Id != resp.Id {
		log.Printf("DNS ID mismatch, request: %d, response: %d\n", req.Id, resp.Id)
		return nil
	}

	var resolved []HostPort
	for _, r := range resp.Answer {
		if qType == dns.TypeA {
			if a, ok := r.(*dns.A); ok {
				ip := a.A.String()
				if debug {
					log.Printf("Resolved `%s` to `%s`\n", name, ip)
				}
				resolved = append(resolved, HostPort{host: ip})
			}
		} else {
			if srv, ok := r.(*dns.SRV); ok {
				target := srv.Target
				port := strconv.Itoa(int(srv.Port))
				if debug {
					log.Printf("Resolved `%s` to `%s`\n", name, net.JoinHostPort(target, port))
				}
				resolved = append(resolved, HostPort{host: target, port: port})
			}
		}
	}

	if verbose && len(resolved) == 0 {
		log.Printf("DNS response has no %s records for `%s`: %+v\n", dns.TypeToString[qType], name, resp)
	}

	return resolved
}

func refreshDns(connectTo []string, dnsUpdates chan []string) {
	var targets []HostPort

	noDnsRequired := true
	for _, target := range connectTo {
		var host, port string
		if srv {
			host = target
		} else {
			var err error
			host, port, err = net.SplitHostPort(target)
			if err != nil {
				log.Fatalf("Error parsing `%s`: %v\n", target, err)
			}
		}
		resolve := host != "" && net.ParseIP(host) == nil
		if noDnsRequired && resolve {
			noDnsRequired = false
		}
		if host != "" {
			host = dns.Fqdn(host)
		}
		targets = append(targets, HostPort{host, port, resolve})
	}

	if noDnsRequired {
		if verbose && dnsServer != "" {
			log.Printf("Only port/IP provided in `%v`, DNS server address is unused\n", connectTo)
		}
		dnsUpdates <- connectTo
		return
	}

	// https://pkg.go.dev/github.com/miekg/dns#Client
	// https://github.com/benschw/dns-clb-go/blob/master/dns/lib.go
	dnsClient := &dns.Client{Net: "tcp"}
	var resolvedTargets []string

	queryDns := func() {
		var newTargets []string
		for _, target := range targets {
			if !target.resolve {
				newTargets = append(newTargets, net.JoinHostPort(target.host, target.port))
				continue
			}

			if srv {
				srvTargets := queryDns(dnsClient, target.host, dns.TypeSRV)
				for _, srvTarget := range srvTargets {
					ips := queryDns(dnsClient, srvTarget.host, dns.TypeA)
					for _, ip := range ips {
						newTargets = append(newTargets, net.JoinHostPort(ip.host, srvTarget.port))
					}
				}
			} else {
				ips := queryDns(dnsClient, target.host, dns.TypeA)
				for _, ip := range ips {
					newTargets = append(newTargets, net.JoinHostPort(ip.host, target.port))
				}
			}
		}

		sort.Strings(newTargets)

		update := false
		if len(resolvedTargets) != len(newTargets) {
			update = true
		}
		if !update {
			for i, newTarget := range newTargets {
				if resolvedTargets[i] != newTarget {
					update = true
					break
				}
			}
		}

		if update {
			dnsUpdates <- newTargets
			if verbose {
				log.Printf("Connect target changed: %v\n", newTargets)
			}
			resolvedTargets = newTargets
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

func manageTcp(resolver chan []string, connections chan net.Conn) {
	var connectTo []string
	var i uint

	for {
		select {
		case connectTo = <-resolver:

		case in := <-connections:
			if len(connectTo) > 0 {
				go forwardTcp(in, connectTo[i%uint(len(connectTo))])
				i++
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

func manageUdp(resolver chan []string, connections chan net.Conn) {
	var in, out net.Conn
	var i uint

	for {
		select {
		case connectTo := <-resolver:
			if out != nil {
				out.Close()
				out = nil
			}
			if len(connectTo) > 0 {
				_out, err := net.Dial("udp", connectTo[i%uint(len(connectTo))])
				i++
				if err != nil {
					log.Printf("Conection to `%s` failed: %v\n", connectTo, err)
				} else {
					out = _out
					if in != nil {
						go forwardUdp(in, out)
					}
				}
			}

		case _in := <-connections:
			in = _in
			if out != nil {
				go forwardUdp(in, out)
			}
		}
	}
}

func forwardUdp(from net.Conn, to net.Conn) {
	for {
		w, err := io.Copy(to, from)
		if debug {
			log.Printf("UDP forwarding interrupted: %v; %v bytes forwarded\n", err, w)
		}
		if strings.Contains(err.Error(), "closed network connection") {
			break
		}
		time.Sleep(1 * time.Second)
	}
}
