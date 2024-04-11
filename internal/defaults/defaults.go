package defaults

import (
	"time"
)

const (
	Do53Server = "1.1.1.1:53"
	Do53Port   = "53"
	DoTServer  = "1.1.1.1:853"
	DoTPort    = "853"
	DoHURL     = "https://cloudflare-dns.com/dns-query"
	Timeout    = 2 * time.Second
	MaxCNAMEs  = 0
)
