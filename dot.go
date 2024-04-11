package dnsclient

import (
	"crypto/tls"
	"fmt"

	"github.com/miekg/dns"
)

type DoTConfig struct {
	Config
	TLSConfig *tls.Config
	Server    string
}

type DoTClient struct {
	config *DoTConfig
	client *dns.Client
	conn   *dns.Conn
}

func NewDoTClient(config *DoTConfig) *DoTClient {
	c := &DoTClient{config: config}
	c.client = &dns.Client{
		Net:     "tcp-tls",
		Timeout: config.Timeout,
	}
	return c
}

func (c *DoTClient) GetConfig() *Config {
	return &c.config.Config
}

func (c *DoTClient) Dial() error {
	var err error
	c.conn, err = c.client.Dial(c.config.Server)
	if err != nil {
		return fmt.Errorf("failed to connect to DNS server: %w", err)
	}
	return nil
}

func (c *DoTClient) Close() error {
	return c.conn.Close()
}

func (c *DoTClient) Query(req *dns.Msg) (*dns.Msg, error) {
	resp, _, err := c.client.ExchangeWithConn(req, c.conn)
	return resp, err
}
