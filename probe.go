package dnsclient

import (
	"fmt"
	"net"

	"github.com/miekg/dns"
	"github.com/syslab-wm/dnsclient/internal/msgutil"
)

func ProbeSupportsEDNS0Subnet(c Client, domainname string) (bool, error) {
	msg := NewMsg(c.GetConfig(), domainname, dns.TypeSOA)

	// create an OPT RR
	o := new(dns.OPT)
	o.Hdr.Name = "."
	o.Hdr.Rrtype = dns.TypeOPT
	e := new(dns.EDNS0_SUBNET)
	e.Code = dns.EDNS0SUBNET // by default this is filled in through unpacking OPT packets (unpackDataOpt)
	e.Family = 1             // 1 for IPv4 source address, 2 for IPv6
	e.SourceNetmask = 24     // 32 for IPV4, 128 for IPv6
	e.SourceScope = 0
	e.Address = net.ParseIP("127.0.0.1").To4() // for IPv4
	// e.Address = net.ParseIP("2001:7b8:32a::2")	// for IPV6
	o.Option = append(o.Option, e)
	msg.Extra = append(msg.Extra, o)

	resp, err := Query(c, msg)
	if err != nil {
		return false, err
	}

	// XXX: could also use resp.IsEdns0 to get the OPT record

	// see if the same OPT RR is returned in the response
	opts := msgutil.CollectRRs[*dns.OPT](resp.Extra)
	if len(opts) == 0 {
		return false, nil
	}

	for _, opt := range opts {
		for _, s := range opt.Option {
			_, ok := s.(*dns.EDNS0_SUBNET)
			if ok {
				return true, nil
			}
		}
	}

	return false, nil
}

func ProbeNSID(c Client, domainname string) (string, error) {
	msg := NewMsg(c.GetConfig(), domainname, dns.TypeSOA)

	// create an OPT RR
	o := new(dns.OPT)
	o.Hdr.Name = "."
	o.Hdr.Rrtype = dns.TypeOPT
	e := new(dns.EDNS0_NSID)
	e.Code = dns.EDNS0NSID
	o.Option = append(o.Option, e)
	msg.Extra = append(msg.Extra, o)

	resp, err := Query(c, msg)
	if err != nil {
		return "", err
	}

	opt := resp.IsEdns0()
	if opt == nil {
		return "", fmt.Errorf("resp does not contain an OPT record")
	}

	for _, s := range opt.Option {
		e, ok := s.(*dns.EDNS0_NSID)
		if ok {
			return e.Nsid, nil
		}
	}

	return "", fmt.Errorf("resp's OPT record does not contain an NSID option")
}
