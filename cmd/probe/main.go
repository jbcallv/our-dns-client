package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/syslab-wm/dnsclient"
	"github.com/syslab-wm/dnsclient/internal/defaults"
	"github.com/syslab-wm/dnsclient/internal/netx"
	"github.com/syslab-wm/mu"
)

const usage = `Usage: probes [options] NAMESERVER DOMAINNAME

Send a DNS request to that probes some feature of a nameserver.

positional arguments:
  NAMESERVER
      The nameserver to query, of the form host[:port].  If port is not given,
      the default port for that particiular protocol is used (i.e., port 53 for
      Do53).

  DOMAINNAME
    The domainname to query.   The probe sends an SOA query for that domainname,
    which should produce a response for any domainname in that zone.
    
general options:
  -type PROBE_TYPE
    The probe type.  Must be one of:
      * nsid (Default)
          ENDS0 NSID. (RFC 5001).  If the nameserver supports this
          extension, the result of the probe is the nameserver's
          unique ID.
        
      * ecs
          EDNS0 Client Subnet support (RFC 7871).  The probe
          reports whether the nameserver supports this feature.

  -proto PROTO
    The DNS protocol to use (case-insensitive).  Must be either:
      * Do53  
          Regular cleartext DNS (DNS-over-(Port)53)
      * DoT
          DNS-over-TLS
      * DoH
          DNS-over-HTTPS

    The default is Do53.

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
    # 216.239.32.10 is an authoritative nameserver for google.com
    $ ./probe -type nsid 216.239.32.10 google.com
`

type Options struct {
	// positional
	server     string
	domainname string
	// general options
	probeType string
	proto     string
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
	flag.StringVar(&opts.probeType, "type", "nsid", "")
	flag.StringVar(&opts.proto, "proto", "do53", "")
	flag.DurationVar(&opts.timeout, "timeout", defaults.Timeout, "")
	flag.IntVar(&opts.maxCNAMEs, "max-cnames", defaults.MaxCNAMEs, "")
	flag.BoolVar(&opts.dnssec, "dnssec", false, "")
	// do53-specific options
	flag.BoolVar(&opts.tcp, "tcp", false, "")
	flag.BoolVar(&opts.retryWithTCP, "retry-with-tcp", false, "")

	flag.Parse()

	if flag.NArg() != 2 {
		mu.Fatalf("error: expected two positional arguments but got %d", flag.NArg())
	}

	opts.server = flag.Arg(0)
	opts.domainname = flag.Arg(1)

	if opts.probeType != "nsid" && opts.probeType != "ecs" {
		mu.Fatalf("error: unrecognized -type %q: must be either \"nsid\" or \"ecs\"", opts.probeType)
	}

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

func doNSIDProbe(c dnsclient.Client, domainname string) {
	nsid, err := dnsclient.ProbeNSID(c, domainname)
	if err != nil {
		mu.Fatalf("query failed: %v", err)
	}

	data, err := hex.DecodeString(nsid)
	if err != nil {
		fmt.Printf("nsid is %s\n", nsid)
	} else {
		fmt.Printf("nsid is %s (%s)\n", nsid, string(data))
	}
}

func doECSProbe(c dnsclient.Client, domainname string) {
	ok, err := dnsclient.ProbeSupportsEDNS0Subnet(c, domainname)
	if err != nil {
		mu.Fatalf("query failed: %v", err)
	}

	if ok {
		fmt.Println("supports ecs")
	} else {
		fmt.Println("does not support ecs")
	}
}

func main() {
	opts := parseOptions()

	c := newClient(opts)
	err := c.Dial()
	if err != nil {
		mu.Fatalf("failed to connect to DNS server: %v", err)
	}
	defer c.Close()

	switch opts.probeType {
	case "nsid":
		doNSIDProbe(c, opts.domainname)
	case "ecs":
		doECSProbe(c, opts.domainname)
	default:
		mu.BUG("opts.probeType must be either \"nsid\" or \"ecs\"; got %q", opts.probeType)
	}
}
