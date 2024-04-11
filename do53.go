package dnsclient

import (
	"fmt"

	"github.com/miekg/dns"
)

type Do53Config struct {
	Config
	UseTCP       bool
	RetryWithTCP bool
	Server       string
}

type Do53Client struct {
	config *Do53Config
	client *dns.Client
	conn   *dns.Conn
}

func NewDo53Client(config *Do53Config) *Do53Client {
	c := &Do53Client{config: config}
	protocol := "" // udp
	if config.UseTCP {
		protocol = "tcp"
	}
	c.client = &dns.Client{
		Net:     protocol,
		Timeout: config.Timeout,
	}

	return c
}

func (c *Do53Client) GetConfig() *Config {
	return &c.config.Config
}

func (c *Do53Client) Dial() error {
	var err error
	c.conn, err = c.client.Dial(c.config.Server)
	if err != nil {
		return fmt.Errorf("failed to connect to DNS server: %w", err)
	}
	return nil
}

func (c *Do53Client) Close() error {
	return c.conn.Close()
}

func (c *Do53Client) Query(req *dns.Msg) (*dns.Msg, error) {
	resp, _, err := c.client.ExchangeWithConn(req, c.conn)
	return resp, err
}
