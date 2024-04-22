package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
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

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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

  -num-workers NUM-WORKERS
    Number of goroutines to launch

  -collection COLLECTION
	MongoDB collection to insert into

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
	collection string
}

type Service struct {
	Name     string
	Priority uint16
	Weight   uint16
	Port     uint16
	Target   string
	Txt      []string
}

type ServiceDiscovery struct {
	Domain          string
	BrowsingDomains []string
	Services        []Service
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
	flag.StringVar(&opts.collection, "collection", "top-1m", "")
	flag.Parse()

	if flag.NArg() != 1 {
		mu.Fatalf("error: expected one positional argument but got %d", flag.NArg())
	}

	opts.inputFile = flag.Arg(0)
	opts.server = tryAddDefaultPort(opts.server, defaults.Do53Port)

	return &opts
}

func connectToDatabase(collection string) *mongo.Client {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		log.Fatal("You must set your 'MONGODB_URI' environment variable. See\n\t https://www.mongodb.com/docs/drivers/go/current/usage-examples/#environment-variable")
	}

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
	if err != nil {
		panic(err)
	}

	return client
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

	mongoClient := connectToDatabase(opts.collection)
	collection := mongoClient.Database("services").Collection(opts.collection)

	defer func() {
		if err := mongoClient.Disconnect(context.TODO()); err != nil {
			panic(err)
		}
	}()

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

				var serviceRecord ServiceDiscovery
				serviceRecord.Domain = domainname

				browsers, err := dnsclient.GetAllServiceBrowserDomains(c, domainname)
				_ = err
				if browsers != nil {
					// fmt -> stringbuilder for outch
					sb.WriteString("Service Browser Domains:\n")

					var browsingDomains []string
					for _, browser := range browsers {
						sb.WriteString(fmt.Sprintf("\t%s\n", browser)) // sprintf returns string
						browsingDomains = append(browsingDomains, browser)
					}
					serviceRecord.BrowsingDomains = browsingDomains
				} else {
					// if we don't find any browsing domains, treat the original
					// domain as the browsing domain
					browsers = []string{domainname}
					serviceRecord.BrowsingDomains = browsers
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

					var listOfServices []Service
					for _, service := range serviceSet.Items() {
						sb.WriteString(fmt.Sprintf("\t%s\n", service))

						instances, err := dnsclient.GetServiceInstances(c, service)
						if err != nil {
							continue
						}
						for _, instance := range instances {
							var service Service
							service.Name = instance

							info, err := dnsclient.GetServiceInstanceInfo(c, instance)
							if err != nil {
								continue
							}

							sb.WriteString(fmt.Sprintf("\t\t%v\n", info))

							service.Priority = info.Priority
							service.Weight = info.Weight
							service.Port = info.Port
							service.Target = info.Target
							service.Txt = info.Txt

							listOfServices = append(listOfServices, service)
						}
					}

					serviceRecord.Services = listOfServices
					result, err := collection.InsertOne(context.TODO(), serviceRecord)
					if err != nil {
						panic(err)
					}
					_ = result
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
