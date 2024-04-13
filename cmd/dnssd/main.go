package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	// added to serve output to outch
	"strings"

	"github.com/syslab-wm/adt/set"
	"github.com/syslab-wm/dnsclient"
	"github.com/syslab-wm/dnsclient/internal/defaults"
	"github.com/syslab-wm/dnsclient/internal/netx"
	"github.com/syslab-wm/mu"
)

const usage = `Usage: dnssd [options] DOMAIN

Attempt to use DNS Service Discovery (DNS-SD) to enumerate the services and
service instances of a given domain.

positional arguments:
  INPUT FILE
    The input file to iterate domains to enumerate services for
    
  -server SERVER
    The nameserver to query.  SERVER is of the form
    IP[:PORT].  If PORT is not provided, then port 53 is used.

    Default: 1.1.1.1:53 (Cloudflare's open resolver)

  -tcp
    Use TCP instead of UDP for issuing DNS queries.

  -timeout TIMEOUT
    The timeout for a DNS query (e.g. 500ms, 1.5s).

    Default: 2s

  -num-workers
    Number of goroutines to launch

  -help
    Display this usage statement and exit.

examples:
  $ ./dnssd www.cs.wm.edu
`

type Options struct {
	// positional
	domain string
	// options
	server     string
	tcp        bool
	timeout    time.Duration
	numWorkers int
	inputFile  string
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
	flag.StringVar(&opts.server, "server", defaults.Do53Server, "")
	flag.BoolVar(&opts.tcp, "tcp", false, "")
	flag.IntVar(&opts.numWorkers, "num-workers", 1, "")
	flag.DurationVar(&opts.timeout, "timeout", defaults.Timeout, "")

	flag.Parse()

	if flag.NArg() != 1 {
		mu.Fatalf("error: expected one positional argument but got %d", flag.NArg())
	}

	opts.inputFile = flag.Arg(0)
	opts.server = tryAddDefaultPort(opts.server, defaults.Do53Port)

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

		// added for csv! Disable if first column is domain name
		domain_information := strings.Split(line, ",")
		line = domain_information[1]
		ch <- line
		i++
	}

	if err := scanner.Err(); err != nil {
		mu.Fatalf("error: failed to read input file: %v", err)
	}
}

func main() {
	opts := parseOptions()

	var wg sync.WaitGroup
	inch := make(chan string, opts.numWorkers)
	outch := make(chan string, opts.numWorkers)
	wg.Add(opts.numWorkers)

	for i := 0; i < opts.numWorkers; i++ {
		go func() {
			config := &dnsclient.Do53Config{
				Config: dnsclient.Config{
					RecursionDesired: true,
					Timeout:          opts.timeout,
				},
				UseTCP: opts.tcp,
				Server: opts.server,
			}

			var c dnsclient.Client
			//c = dnsclient.NewDo53Client(config)

			defer func() {
				wg.Done()
				if c != nil {
					c.Close()
				}
			}()

			c = dnsclient.NewDo53Client(config)

			err := c.Dial()
			if err != nil {
				mu.Fatalf("failed to connect to DNS server: %v", err)
			}
			defer c.Close()

			for domainname := range inch {
				var sb strings.Builder
				sb.WriteString("\n" + domainname + "\n")

				browsers, err := dnsclient.GetAllServiceBrowserDomains(c, domainname)
				_ = err
				if browsers != nil {
					// fmt -> stringbuilder for outch
					sb.WriteString("Service Browser Domains:\n")
					for _, browser := range browsers {
						sb.WriteString(fmt.Sprintf("\t%s\n", browser)) // sprintf returns string
					}
				} else {
					// if we don't find any browsing domains, treat the original
					// domain as the browsing domain
					browsers = []string{domainname}
				}

				serviceSet := set.New[string]()
				for _, browser := range browsers {
					services, err := dnsclient.GetServices(c, browser)
					if err != nil {
						continue
					}
					serviceSet.Add(services...)
				}

				services := serviceSet.Items()

				if len(services) != 0 {
					sb.WriteString("Services:\n")
					for _, service := range serviceSet.Items() {
						sb.WriteString(fmt.Sprintf("\t%s\n", service))
						instances, err := dnsclient.GetServiceInstances(c, service)
						if err != nil {
							continue
						}
						for _, instance := range instances {
							info, err := dnsclient.GetServiceInstanceInfo(c, instance)
							if err != nil {
								continue
							}
							sb.WriteString(fmt.Sprintf("\t\t%v\n", info))
						}
					}
				}

				outputString := sb.String()
				outch <- outputString
			}
		}()
	}

	go func() {
		wg.Wait()
		close(outch)
	}()

	go processFile(opts.inputFile, inch)

	numJobs := 0
	for outString := range outch {
		fmt.Printf(outString)
		numJobs += 1
	}

	fmt.Printf("processed all %d jobs\n", numJobs)
}
