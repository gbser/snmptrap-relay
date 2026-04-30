package forward

import (
	"fmt"
	"net"
	"strconv"

	"snmptrap-relay/internal/model"
)

type Target struct {
	Config model.ForwarderConfig
	Addr   *net.UDPAddr
	conn   *net.UDPConn
}

type UDPForwarder struct {
	targets []Target
}

func NewUDP(cfg []model.ForwarderConfig) (*UDPForwarder, error) {
	f := &UDPForwarder{}
	for _, item := range cfg {
		if !item.Enabled {
			continue
		}
		addr, err := resolve(item.Host, item.Port)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("forwarder %q: %w", item.Name, err)
		}
		conn, err := newTargetConn(item.SourceHost, addr)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("forwarder %q: %w", item.Name, err)
		}
		f.targets = append(f.targets, Target{Config: item, Addr: addr, conn: conn})
	}
	return f, nil
}

func resolve(host string, port int) (*net.UDPAddr, error) {
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return addr, nil
}

func newTargetConn(sourceHost string, targetAddr *net.UDPAddr) (*net.UDPConn, error) {
	network := udpNetwork(targetAddr)
	if sourceHost == "" {
		return net.ListenUDP(network, nil)
	}
	localAddr, err := net.ResolveUDPAddr(network, net.JoinHostPort(sourceHost, "0"))
	if err != nil {
		return nil, err
	}
	return net.ListenUDP(network, localAddr)
}

func udpNetwork(addr *net.UDPAddr) string {
	if addr != nil && addr.IP.To4() == nil && addr.IP.To16() != nil {
		return "udp6"
	}
	return "udp4"
}

func (f *UDPForwarder) Targets() []Target {
	out := make([]Target, len(f.targets))
	copy(out, f.targets)
	return out
}

func (f *UDPForwarder) Send(payload []byte) error {
	var errs []error
	for _, target := range f.targets {
		if _, err := target.conn.WriteToUDP(payload, target.Addr); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target.Config.Name, err))
		}
	}
	if len(errs) > 0 {
		return joinErrors(errs)
	}
	return nil
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	msg := errs[0].Error()
	for i := 1; i < len(errs); i++ {
		msg += "; " + errs[i].Error()
	}
	return fmt.Errorf("%s", msg)
}

func (f *UDPForwarder) Close() error {
	var errs []error
	for _, target := range f.targets {
		if target.conn == nil {
			continue
		}
		if err := target.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return joinErrors(errs)
}
