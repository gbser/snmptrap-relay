package snmp

import (
	"strings"
	"testing"

	"snmptrap-relay/internal/ber"
)

func TestParseTrapV2(t *testing.T) {
	trapOID := "1.3.6.1.4.1.9999.0.10"
	deviceOID := "1.3.6.1.4.1.9999.1.1"
	pkt := buildV2Trap(t, "public", []varBind{
		{OID: "1.3.6.1.2.1.1.3.0", Value: tlv(0x43, encodeIntegerBytes(12345))},
		{OID: "1.3.6.1.6.3.1.1.4.1.0", Value: encOID(t, trapOID)},
		{OID: deviceOID, Value: tlv(0x02, encodeIntegerBytes(7))},
	})

	event, err := ParseTrap(pkt, "10.0.0.1", 5555, map[string]string{deviceOID: "ifIndex"})
	if err != nil {
		t.Fatalf("ParseTrap() error = %v", err)
	}

	if got, want := event.TrapOID, trapOID; got != want {
		t.Fatalf("TrapOID = %q, want %q", got, want)
	}
	if got, want := event.Fields["trap_oid"], trapOID; got != want {
		t.Fatalf("fields.trap_oid = %q, want %q", got, want)
	}
	if got, want := event.Fields["fields.ifIndex"], "7"; got != want {
		t.Fatalf("fields.ifIndex = %q, want %q", got, want)
	}
	if got, want := event.Fields["varbind."+deviceOID], "7"; got != want {
		t.Fatalf("varbind field = %q, want %q", got, want)
	}
	if !strings.Contains(event.Summary(), "trap_oid="+trapOID) {
		t.Fatalf("Summary() did not contain trap OID: %s", event.Summary())
	}
}

type varBind struct {
	OID   string
	Value []byte
}

func buildV2Trap(t *testing.T, community string, binds []varBind) []byte {
	t.Helper()
	var vbSeq [][]byte
	for _, vb := range binds {
		vbSeq = append(vbSeq, encodeVarBind(vb.OID, vb.Value))
	}
	varbindList := seq(vbSeq...)
	var pduBody []byte
	pduBody = append(pduBody, tlv(0x02, encodeIntegerBytes(1))...)
	pduBody = append(pduBody, tlv(0x02, encodeIntegerBytes(0))...)
	pduBody = append(pduBody, tlv(0x02, encodeIntegerBytes(0))...)
	pduBody = append(pduBody, varbindList...)
	pdu := tlv(0xA7, pduBody)
	msg := seq(
		tlv(0x02, encodeIntegerBytes(1)),
		tlv(0x04, []byte(community)),
		pdu,
	)
	return msg
}

func encodeVarBind(oid string, value []byte) []byte {
	return seq(encOID(nil, oid), value)
}

func encOID(t *testing.T, oid string) []byte {
	if t != nil {
		t.Helper()
	}
	encoded, err := encodeOID(oid)
	if err != nil {
		if t != nil {
			t.Fatalf("encodeOID(%q): %v", oid, err)
		}
		panic(err)
	}
	return tlv(0x06, encoded)
}

func encodeOID(oid string) ([]byte, error) {
	parts := strings.Split(oid, ".")
	if len(parts) < 2 {
		return nil, &ber.Error{Msg: "OID needs at least two nodes"}
	}
	first := mustAtoi(parts[0])
	second := mustAtoi(parts[1])
	buf := []byte{byte(first*40 + second)}
	for _, part := range parts[2:] {
		n := mustAtoi(part)
		buf = append(buf, encodeBase128(n)...)
	}
	return buf, nil
}

func encodeBase128(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0x7F)}, tmp...)
		n >>= 7
	}
	for i := 0; i < len(tmp)-1; i++ {
		tmp[i] |= 0x80
	}
	return tmp
}

func mustAtoi(s string) int {
	n := 0
	for _, ch := range s {
		n = n*10 + int(ch-'0')
	}
	return n
}

func encodeIntegerBytes(n int64) []byte {
	if n == 0 {
		return []byte{0}
	}
	negative := n < 0
	var buf []byte
	for n != 0 && n != -1 {
		buf = append([]byte{byte(n & 0xFF)}, buf...)
		n >>= 8
	}
	if negative && buf[0]&0x80 == 0 {
		buf = append([]byte{0xFF}, buf...)
	}
	if !negative && buf[0]&0x80 != 0 {
		buf = append([]byte{0x00}, buf...)
	}
	return buf
}

func tlv(tag byte, value []byte) []byte {
	return append([]byte{tag}, append(encodeLength(len(value)), value...)...)
}

func seq(parts ...[]byte) []byte {
	var body []byte
	for _, part := range parts {
		body = append(body, part...)
	}
	return tlv(0x30, body)
}

func encodeLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0xFF)}, tmp...)
		n >>= 8
	}
	return append([]byte{0x80 | byte(len(tmp))}, tmp...)
}
