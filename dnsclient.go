package dnsclient

import (
	"fmt"
	"time"

	"github.com/miekg/dns"
	"github.com/syslab-wm/dnsclient/internal/msgutil"
)

// This is configuration that applies to all typs of clients -- it deals purely
// with the handling of the DNS requests and responses
type Config struct {
	IdFunc           func() uint16
	RecursionDesired bool
	Timeout          time.Duration
	MaxCNAMEs        int
	DNSSEC           bool
}

type Client interface {
	GetConfig() *Config
	Dial() error
	Close() error
	Query(req *dns.Msg) (*dns.Msg, error)
}

type DNSErr int

const (
	DNSErrRcodeNotSuccess DNSErr = iota

	DNSErrMissingAnswer
	DNSErrInvalidAnswer
	DNSErrInvalidCNAMEChain
	DNSErrMaxCNAMEs

	DNSErrBadFormatAnswer
)

var DNSErrToString = map[DNSErr]string{
	DNSErrRcodeNotSuccess: "RCODE was not SUCCESS",

	DNSErrMissingAnswer:     "DNS response does not answer the query",
	DNSErrInvalidAnswer:     "DNS response has an answer that matches neither the qname nor one of its aliases",
	DNSErrInvalidCNAMEChain: "DNS response contains an invalid CNAME chain",
	DNSErrMaxCNAMEs:         "query followed max number of CNAMEs",

	DNSErrBadFormatAnswer: "DNS response has an answer where the data does not conform to the RR type",
}

type DNSError struct {
	Reason   DNSErr
	Response *dns.Msg // optional
}

func NewDNSError(reason DNSErr, response *dns.Msg) *DNSError {
	return &DNSError{Reason: reason, Response: response}
}

func (e *DNSError) Error() string {
	if e.Reason == DNSErrRcodeNotSuccess {
		return fmt.Sprintf("%s: %s (rcode=%d)", DNSErrToString[e.Reason],
			dns.RcodeToString[e.Response.Rcode], e.Response.Rcode)
	}
	return fmt.Sprintf("%s", DNSErrToString[e.Reason])
}

func NewMsg(config *Config, name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.Id = dns.Id()
	m.RecursionDesired = config.RecursionDesired
	if config.DNSSEC {
		m.SetEdns0(4096, true)
	}
	// XXX: set other header bits?
	return m
}

func query(c Client, req *dns.Msg) (*dns.Msg, error) {
	resp, err := c.Query(req)
	if err != nil {
		return nil, err
	}
	if resp.Rcode != dns.RcodeSuccess {
		return nil, NewDNSError(DNSErrRcodeNotSuccess, resp)
	}
	return resp, nil
}

// Return an error if:
//   - there was some sort of network error
//   - DNS returned a valid response but Rcode is not SUCCESS
//   - DNS returned Rcode SUCCESS, but the response doesn't have the record we requested
//   - The config is set to follow CNAMES, but we encountered some sort of
//     malformed CNAME (namely, an invalid CNAME chain in an answer; this should be rare)
//   - The config is set to follow CNAMES, and we reached MaxCNAMEs
//     without getting an answer
//
// In other words, if we ergonomics are such that if the caller invokes:
//
//	resp, err != dnsquery.Query(c, name, type)
//
// Then err == nil iff resp actually returns a positive response for the query.
// If err != nil, then err will store the response message (if there was one)
// as well as indicate the reason for the failure (basically, one of the above
// categories above.  The whole point is to make it so the caller doesn't have
// to inspect the msg to see if the query succeeded; if the caller wants the
// nitty-gritty details of why the query didn't get an answer, it can inspect
// the error value.
func Query(c Client, req *dns.Msg) (*dns.Msg, error) {
	var err error
	var cnames []*dns.CNAME
	var resp *dns.Msg
	config := c.GetConfig()
	qtype := req.Question[0].Qtype

	// if following CNAMES, req will change; thus, make a copy so it
	// doesn't affect the caller
	if config.MaxCNAMEs > 0 {
		req = req.Copy()
	}

	for i := 0; i <= config.MaxCNAMEs; i++ {
		resp, err = query(c, req)
		if err != nil {
			return nil, err
		}

		// gather all RRs that are of the qtype
		var ans []dns.RR
		for _, rr := range resp.Answer {
			if rr.Header().Rrtype == qtype {
				ans = append(ans, rr)
				// if such an RR matches on the name we're searching for, it's a
				// direct hit
				if rr.Header().Name == req.Question[0].Name {
					return resp, nil
				}
			}
		}

		// if we're following CNAMEs, get all of the CNAMES from the answer
		cnames = msgutil.CollectRRs[*dns.CNAME](resp.Answer)
		if len(cnames) == 0 {
			return nil, NewDNSError(DNSErrMissingAnswer, resp)
		}

		// validate that the CNAMEs form a chain
		ordered := msgutil.OrderCNAMEs(cnames)
		if !ordered {
			return nil, NewDNSError(DNSErrInvalidCNAMEChain, resp)

		}
		// the head of the chain must match the name we're searching for
		if cnames[0].Hdr.Name != req.Question[0].Name {
			return nil, NewDNSError(DNSErrInvalidCNAMEChain, resp)
		}

		// is the last CNAME in chain an alias for one of the RRs that are of
		// the qtype we're searching for.  If so, success.
		lastCNAME := cnames[len(cnames)-1]
		for _, rr := range ans {
			if lastCNAME.Target == rr.Header().Name {
				return resp, nil
			}
		}

		if len(ans) > 0 {
			// a really weird case: the resp has record types we're searching
			// for, but not for an alias of a name we're searching for
			return nil, NewDNSError(DNSErrInvalidAnswer, resp)
		}

		// update the domain name to query; TODO: get Qtype
		req.SetQuestion(dns.Fqdn(cnames[len(cnames)-1].Target), qtype)
	}

	if len(cnames) > 0 {
		return nil, NewDNSError(DNSErrMaxCNAMEs, resp)
	}

	return nil, NewDNSError(DNSErrMissingAnswer, resp)
}

func Lookup(c Client, name string, qtype uint16) (*dns.Msg, error) {
	req := NewMsg(c.GetConfig(), name, qtype)
	return Query(c, req)
}
