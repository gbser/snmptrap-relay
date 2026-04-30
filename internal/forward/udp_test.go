package forward

import (
	"net"
	"testing"
	"time"

	"snmptrap-relay/internal/model"
)

func TestNewUDPBindsConfiguredSourceHost(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer listener.Close()

	fwd, err := NewUDP([]model.ForwarderConfig{{
		Name:       "target",
		Host:       "127.0.0.1",
		Port:       listener.LocalAddr().(*net.UDPAddr).Port,
		Enabled:    true,
		SourceHost: "127.0.0.1",
	}})
	if err != nil {
		t.Fatalf("NewUDP() error = %v", err)
	}
	defer fwd.Close()

	if err := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := fwd.Send([]byte("trap")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	buf := make([]byte, 32)
	_, remote, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP() error = %v", err)
	}
	if got, want := remote.IP.String(), "127.0.0.1"; got != want {
		t.Fatalf("remote IP = %q, want %q", got, want)
	}
}
