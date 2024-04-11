package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/syslab-wm/dnsclient"
	"github.com/syslab-wm/dnsclient/internal/defaults"
	"github.com/syslab-wm/dnsclient/internal/netx"
	"github.com/syslab-wm/mu"
)

const usage = `prodcon2 [options] INPUT_FILE

Example of a producer-consumer problem.

positional
  INPUT_FILE
    Input file, where each line is a domainname

options:
  -num-workers
    Number of worker goroutines

  -help
    Display this usage statement and exit.

general client options:
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

  -qtype QTYPE
    The query type (e.g., A, AAAA, NS)

    Default: A

  -timeout TIMEOUT
    The timeout for the DNS request (e.g. 500ms, 1.5s).

    Default: 2s

  -max-cnames N
    The maximum of number of CNAMEs to follow.

    Default: 0

  -dnssec
    Request DNSSEC records be sent by setting the DNSSEC OK bit (DO) in the OPT
    record in the additional section of the query.


Do53 client-specific options:
  -tcp
    For Do53, use TCP instead of UDP.

  -retry-with-tcp
    For Do53 using UDP, if the DNS response is truncated, then
    re-issue the query over TCP.
`

type Options struct {
	// positional
	inputFile string
	// general options
	numWorkers int
	// general client opts
	proto     string
	server    string
	qtypeStr  string
	qtype     uint16 // derived
	timeout   time.Duration
	maxCNAMEs int
	dnssec    bool
	// do53 client-specific options
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
	var ok bool
	opts := Options{}

	flag.Usage = printUsage
	// options
	flag.IntVar(&opts.numWorkers, "num-workers", 1, "")
	// general client options
	flag.StringVar(&opts.proto, "proto", "do53", "")
	flag.StringVar(&opts.server, "server", "", "")
	flag.StringVar(&opts.qtypeStr, "qtype", "A", "")
	flag.DurationVar(&opts.timeout, "timeout", defaults.Timeout, "")
	flag.IntVar(&opts.maxCNAMEs, "max-cnames", defaults.MaxCNAMEs, "")
	flag.BoolVar(&opts.dnssec, "dnssec", false, "")
	// do53 client-specific options
	flag.BoolVar(&opts.tcp, "tcp", false, "")
	flag.BoolVar(&opts.retryWithTCP, "retry-with-tcp", false, "")

	flag.Parse()

	if flag.NArg() != 1 {
		mu.Fatalf("error: expected one positional argument but got %d", flag.NArg())
	}

	opts.inputFile = flag.Arg(0)

	opts.proto = strings.ToLower(opts.proto)
	if opts.proto != "do53" && opts.proto != "dot" && opts.proto != "doh" {
		mu.Fatalf("error: unrecognized proto %q: must be either \"do53\", \"dot\", or \"doh\"", opts.proto)
	}

	opts.qtypeStr = strings.ToUpper(opts.qtypeStr)
	opts.qtype, ok = dns.StringToType[opts.qtypeStr]
	if !ok {
		mu.Fatalf("error: invalid qtype %q", opts.qtypeStr)
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

func processFile(path string, ch chan<- string) {
	defer close(ch)

	f, err := os.Open(path)
	if err != nil {
		mu.Fatalf("failed to open input file: %v", err)
	}
	defer f.Close()

	i := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		ch <- line
		i++
	}

	if err := scanner.Err(); err != nil {
		mu.Fatalf("error: failed to read input file: %v", err)
	}
}

type ScanRecord struct {
	qname string
	qtype uint16

	reply *dns.Msg
	err   error
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
	var wg sync.WaitGroup

	opts := parseOptions()
	inch := make(chan string, opts.numWorkers)
	outch := make(chan *ScanRecord, opts.numWorkers)
	wg.Add(opts.numWorkers)

	for i := 0; i < opts.numWorkers; i++ {
		// each one of these goroutine is a "worker"
		go func() {
			var c dnsclient.Client
			defer func() {
				wg.Done()
				if c != nil {
					c.Close()
				}
			}()

			c = newClient(opts)
			err := c.Dial()
			if err != nil {
				log.Printf("failed to connect to DNS server: %v", err)
				return
			}

			for domainname := range inch {
				reply, err := dnsclient.Lookup(c, domainname, opts.qtype)
				outch <- &ScanRecord{
					qname: domainname,
					qtype: opts.qtype,
					reply: reply,
					err:   err,
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(outch)
	}()

	go processFile(opts.inputFile, inch)

	numJobs := 0
	for rec := range outch {
		if rec.err != nil {
			fmt.Printf("%32s: request failed: %v\n", rec.qname, rec.err)
			continue
		}

		fmt.Printf("%32s: success: %v\n", rec.qname, rec.reply.Answer)

		numJobs += 1
	}

	fmt.Printf("processed all %d jobs\n", numJobs)
}
