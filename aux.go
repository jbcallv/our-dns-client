package dnsclient

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/miekg/dns"
	"github.com/syslab-wm/dnsclient/internal/msgutil"
	"github.com/syslab-wm/functools"
	"github.com/syslab-wm/mu"
)

func lookupA(c Client, domain string) ([]*dns.A, error) {
	resp, err := Lookup(c, domain, dns.TypeA)
	if err != nil {
		return nil, err
	}
	return msgutil.CollectRRs[*dns.A](resp.Answer), nil
}

func getA(c Client, domain string) ([]netip.Addr, error) {
	var addrs []netip.Addr

	as, err := lookupA(c, domain)
	if err != nil {
		return nil, err
	}

	for _, a := range as {
		addr, ok := netip.AddrFromSlice(a.A)
		if !ok {
			continue
		}
		addrs = append(addrs, addr)
	}

	if len(addrs) == 0 {
		return nil, NewDNSError(DNSErrBadFormatAnswer, nil)
	}

	return addrs, nil
}

func lookupAAAA(c Client, domain string) ([]*dns.AAAA, error) {
	resp, err := Lookup(c, domain, dns.TypeAAAA)
	if err != nil {
		return nil, err
	}
	return msgutil.CollectRRs[*dns.AAAA](resp.Answer), nil
}

func getAAAA(c Client, domain string) ([]netip.Addr, error) {
	var addrs []netip.Addr

	as, err := lookupAAAA(c, domain)
	if err != nil {
		return nil, err
	}

	for _, a := range as {
		addr, ok := netip.AddrFromSlice(a.AAAA)
		if !ok {
			continue
		}
		addrs = append(addrs, addr)
	}

	if len(addrs) == 0 {
		return nil, NewDNSError(DNSErrBadFormatAnswer, nil)
	}

	return addrs, nil
}

func GetIP4s(c Client, name string) ([]netip.Addr, error) {
	return getA(c, name)
}

func GetIP6s(c Client, name string) ([]netip.Addr, error) {
	return getAAAA(c, name)
}

func GetIPs(c Client, name string) ([]netip.Addr, error) {
	var addrs []netip.Addr
	var errs []error

	a, err := GetIP4s(c, name)
	if err != nil {
		errs = append(errs, err)
	} else {
		addrs = append(addrs, a...)
	}

	a, err = GetIP6s(c, name)
	if err != nil {
		errs = append(errs, err)
	} else {
		addrs = append(addrs, a...)
	}

	if len(addrs) > 0 {
		return addrs, nil
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	mu.BUG("neither addresses nor errors")
	return nil, nil
}

type NameServer struct {
	Name  string
	Addrs []netip.Addr
}

func (ns *NameServer) String() string {
	return fmt.Sprintf("name: %s, addrs: %v", ns.Name, ns.Addrs)
}

func lookupNS(c Client, domain string) ([]*dns.NS, error) {
	resp, err := Lookup(c, domain, dns.TypeNS)
	if err != nil {
		return nil, err
	}
	return msgutil.CollectRRs[*dns.NS](resp.Answer), nil
}

func getNS(c Client, domain string) ([]string, error) {
	var nameServers []string

	nses, err := lookupNS(c, domain)
	if err == nil {
		nameServers = functools.Map[*dns.NS, string](nses, func(ns *dns.NS) string {
			return ns.Ns
		})
		return nameServers, nil
	}

	// check if the query returned RCode success, but failed because there
	// simply wasn't an answer.  In such a case, see if the Authority section
	// has an SOA entry, and return the nameserver in that entry
	e, ok := err.(*DNSError)
	if !ok {
		return nil, err
	}

	if e.Reason == DNSErrRcodeNotSuccess {
		return nil, err
	}

	resp := e.Response
	if resp == nil {
		mu.BUG("expected DNSError to have a non-nil Response field")
	}

	soas := msgutil.CollectRRs[*dns.SOA](resp.Ns)
	if len(soas) == 0 {
		return nil, err
	}

	nameServers = functools.Map[*dns.SOA, string](soas, func(soa *dns.SOA) string {
		return soa.Ns
	})

	return nameServers, nil
}

func GetNameServers(c Client, name string) ([]*NameServer, error) {
	var addrErrs []error
	var results []*NameServer

	nameServers, err := getNS(c, name)
	if err != nil {
		return nil, err
	}

	for _, nameServer := range nameServers {
		addrs, err := GetIPs(c, nameServer)
		if err != nil {
			addrErrs = append(addrErrs, err)
			continue
		}
		results = append(results, &NameServer{Name: nameServer, Addrs: addrs})
	}

	if len(results) > 0 {
		return results, nil
	}

	if len(addrErrs) > 0 {
		return nil, errors.Join(addrErrs...)
	}

	mu.BUG("neither addresses nor errors")
	return nil, nil
}
