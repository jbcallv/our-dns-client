package main

import (
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/syslab-wm/dnsclient"
	"github.com/syslab-wm/dnsclient/internal/defaults"
	"github.com/syslab-wm/dnsclient/internal/netx"
	"github.com/syslab-wm/functools"
	"github.com/syslab-wm/mu"
)

const usage = `Usage: getnameservers [options] DOMAINNAME

Get a list of namservers (their domainnames and IP addresses) for a given domainname.

positional arguments:
  DOMAINNAME
    The domainname to get the nameservers for
    
general options:
  -proto PROTO
    The DNS protocol to use (case-insensitive).  Must be either:
      * Do53  
          Regular cleartext DNS (DNS-over-(Port)53)
      * DoT
          DNS-over-TLS
      * DoH
          DNS-over-HTTPS

    The default is Do53.

  -server SERVER
    The nameserver to query.  For Do53 and DoH, SERVER is of the form
    IP[:PORT].  If PORT is not provided, then port 53 is used for Do53
    and port 853 is used for DoT.  For DoH, SERVER is the URL of the
    DoH service.

    The default is to use CloudFlare's open resolver at 1.1.1.1
    (for DoH, the URL is https://cloudflare-dns.com/dns-query).

    Default: 1.1.1.1 (Cloudflare's open resolver)

  -timeout TIMEOUT
    The timeout for the DNS request (e.g. 500ms, 1.5s).

    Default: 2s

  -max-cnames N
    The maximum of number of CNAMEs to follow.

    Default: 0

  -dnssec
    Request DNSSEC records be sent by setting the DNSSEC OK bit (DO) in the OPT
    record in the additional section of the query.

  -help
    Display this usage statement and exit.

Do53-specific options:
  -tcp
    For Do53, use TCP instead of UDP.

  -retry-with-tcp
    For Do53 using UDP, if the DNS response is truncated, then
    re-issue the query over TCP.


examples:
  $ ./getnameservesr www.cs.wm.edu
`

type Options struct {
	// positional
	domainname string
	// general options
	proto     string
	server    string
	timeout   time.Duration
	maxCNAMEs int
	dnssec    bool
	// do53-specific options
	tcp          bool
	retryWithTCP bool
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "%s", usage)
}

func tryAddDefaultPort(server string, port string) string {
	if netx.HasPort(server) {
		return server
	}
	return net.JoinHostPort(server, port)
}

func parseOptions() *Options {
	opts := Options{}

	flag.Usage = printUsage
	// general options
	flag.StringVar(&opts.proto, "proto", "do53", "")
	flag.StringVar(&opts.server, "server", "", "")
	flag.DurationVar(&opts.timeout, "timeout", defaults.Timeout, "")
	flag.IntVar(&opts.maxCNAMEs, "max-cnames", defaults.MaxCNAMEs, "")
	flag.BoolVar(&opts.dnssec, "dnssec", false, "")
	// do53-specific options
	flag.BoolVar(&opts.tcp, "tcp", false, "")
	flag.BoolVar(&opts.retryWithTCP, "retry-with-tcp", false, "")

	flag.Parse()

	if flag.NArg() != 1 {
		mu.Fatalf("error: expected one positional argument but got %d", flag.NArg())
	}

	opts.domainname = flag.Arg(0)

	opts.proto = strings.ToLower(opts.proto)
	if opts.proto != "do53" && opts.proto != "dot" && opts.proto != "doh" {
		mu.Fatalf("error: unrecognized proto %q: must be either \"do53\", \"dot\", or \"doh\"", opts.proto)
	}

	if opts.proto == "do53" {
		if opts.tcp && opts.retryWithTCP {
			mu.Fatalf("error: can't specify both -tcp and -retry-with-tcp")
		}

		if opts.server == "" {
			opts.server = defaults.Do53Server
		} else {
			opts.server = tryAddDefaultPort(opts.server, defaults.Do53Port)
		}
	}

	if opts.proto != "do53" {
		if opts.tcp {
			mu.Fatalf("error: -tcp is only valid for -proto do53")
		}
		if opts.retryWithTCP {
			mu.Fatalf("error: -retry-with-tcp is only valid for -proto do53")
		}
	}

	if opts.proto == "dot" {
		if opts.server == "" {
			opts.server = defaults.DoTServer
		} else {
			opts.server = tryAddDefaultPort(opts.server, defaults.DoTPort)
		}
	}

	if opts.proto == "doh" {
		if opts.server == "" {
			opts.server = defaults.DoHURL
		}
		// TODO: parse the opts.server URL to make sure it is a valid HTTPS url
	}

	return &opts
}

func newClient(opts *Options) dnsclient.Client {
	var c dnsclient.Client

	baseConfig := dnsclient.Config{
		RecursionDesired: true,
		Timeout:          opts.timeout,
		MaxCNAMEs:        opts.maxCNAMEs,
		DNSSEC:           opts.dnssec,
	}

	switch opts.proto {
	case "do53":
		config := &dnsclient.Do53Config{
			Config:       baseConfig,
			UseTCP:       opts.tcp,
			RetryWithTCP: opts.retryWithTCP,
			Server:       opts.server,
		}
		c = dnsclient.NewDo53Client(config)
	case "dot":
		config := &dnsclient.DoTConfig{
			Config: baseConfig,
			Server: opts.server,
		}
		c = dnsclient.NewDoTClient(config)
	case "doh":
		config := &dnsclient.DoHConfig{
			Config: baseConfig,
			URL:    opts.server,
		}
		c = dnsclient.NewDoHClient(config)
	default:
		mu.BUG("invalid proto %q", opts.proto)
	}

	return c
}

func main() {
	opts := parseOptions()

	c := newClient(opts)
	err := c.Dial()
	if err != nil {
		mu.Fatalf("failed to connect to DNS server: %v", err)
	}
	defer c.Close()

	nameServers, err := dnsclient.GetNameServers(c, opts.domainname)
	if err != nil {
		mu.Fatalf("query failed: %v", err)
	}

	for _, nameServer := range nameServers {
		strAddrs := functools.Map[netip.Addr, string](nameServer.Addrs, func(addr netip.Addr) string {
			return fmt.Sprintf("%v", addr)
		})
		fmt.Printf("%s: %s\n", nameServer.Name, strings.Join(strAddrs, " "))
	}
}
