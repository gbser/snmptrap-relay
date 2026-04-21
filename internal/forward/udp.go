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
}

type UDPForwarder struct {
	conn    *net.UDPConn
	targets []Target
}

func NewUDP(cfg []model.ForwarderConfig) (*UDPForwarder, error) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}

	f := &UDPForwarder{conn: conn}
	for _, item := range cfg {
		if !item.Enabled {
			continue
		}
		addr, err := resolve(item.Host, item.Port)
		if err != nil {
			return nil, fmt.Errorf("forwarder %q: %w", item.Name, err)
		}
		f.targets = append(f.targets, Target{Config: item, Addr: addr})
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

func (f *UDPForwarder) Targets() []Target {
	out := make([]Target, len(f.targets))
	copy(out, f.targets)
	return out
}

func (f *UDPForwarder) Send(payload []byte) error {
	var errs []error
	for _, target := range f.targets {
		if _, err := f.conn.WriteToUDP(payload, target.Addr); err != nil {
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
	if f.conn == nil {
		return nil
	}
	return f.conn.Close()
}
