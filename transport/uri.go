package transport

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

const (
	_ protocol = iota
	protoTCP
	protoWebsocket
)

var (
	regURI = regexp.MustCompile("^(tcp://|ws://)?([^/:]+):([1-9][0-9]+)$")

	protoMap = map[protocol]string{
		protoTCP:       "TCP",
		protoWebsocket: "Websocket",
	}
)

// URI is used to create a RSocket transport.
type URI struct {
	proto protocol
	host  string
	port  int
}

func (p *URI) String() string {
	return fmt.Sprintf("URI{protocol=%s, host=%s, port=%d}", p.proto, p.host, p.port)
}

// MakeClientTransport returns a new RSocket transport.
func (p *URI) MakeClientTransport(keepaliveInterval, keepaliveMaxLifetime time.Duration) (Transport, error) {
	switch p.proto {
	case protoTCP:
		return NewClientTransportTCP(fmt.Sprintf("%s:%d", p.host, p.port), keepaliveInterval, keepaliveMaxLifetime)
	case protoWebsocket:
		return nil, fmt.Errorf("TODO: support websocket")
	}
	return nil, fmt.Errorf("rsocket: cannot create client transport")
}

type protocol int8

func (s protocol) String() string {
	found, ok := protoMap[s]
	if !ok {
		panic(fmt.Errorf("rsocket: unknown transport protocol %d", s))
	}
	return found
}

// ParseURI parse URI string and returns a URI.
func ParseURI(uri string) (*URI, error) {
	mat := regURI.FindStringSubmatch(uri)
	if mat == nil {
		return nil, fmt.Errorf("rsocket: invalid URI %s", uri)
	}
	proto := mat[1]
	host := mat[2]
	port, _ := strconv.Atoi(mat[3])
	switch proto {
	case "tcp://", "":
		return &URI{
			proto: protoTCP,
			host:  host,
			port:  port,
		}, nil
	case "ws://":
		return &URI{
			proto: protoWebsocket,
			host:  host,
			port:  port,
		}, nil
	default:
		return nil, fmt.Errorf("rsocket: unsupported protocol %s", proto)
	}
}
