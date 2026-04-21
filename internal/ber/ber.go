package ber

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"unicode/utf8"
)

type Error struct {
	Msg string
}

func (e *Error) Error() string { return e.Msg }

type TLV struct {
	Tag    byte
	Length int
	Value  []byte
}

type Reader struct {
	data []byte
	pos  int
}

func NewReader(data []byte) *Reader {
	return &Reader{data: data}
}

func (r *Reader) Remaining() int {
	return len(r.data) - r.pos
}

func (r *Reader) EOF() bool {
	return r.pos >= len(r.data)
}

func (r *Reader) ReadTLV() (TLV, error) {
	if r.Remaining() < 2 {
		return TLV{}, &Error{Msg: "truncated BER data"}
	}

	tag := r.data[r.pos]
	r.pos++
	firstLen := r.data[r.pos]
	r.pos++

	length := 0
	if firstLen < 0x80 {
		length = int(firstLen)
	} else {
		count := int(firstLen & 0x7F)
		if count == 0 {
			return TLV{}, &Error{Msg: "indefinite BER length is not supported"}
		}
		if r.Remaining() < count {
			return TLV{}, &Error{Msg: "truncated BER length"}
		}
		for i := 0; i < count; i++ {
			length = (length << 8) | int(r.data[r.pos+i])
		}
		r.pos += count
	}

	if r.Remaining() < length {
		return TLV{}, &Error{Msg: "truncated BER value"}
	}

	value := make([]byte, length)
	copy(value, r.data[r.pos:r.pos+length])
	r.pos += length
	return TLV{Tag: tag, Length: length, Value: value}, nil
}

func DecodeTagClass(tag byte) string {
	switch tag & 0xC0 {
	case 0x00:
		return "universal"
	case 0x40:
		return "application"
	case 0x80:
		return "context"
	default:
		return "private"
	}
}

func DecodeTagNumber(tag byte) byte {
	return tag & 0x1F
}

func DecodeSignedBigInt(data []byte) *big.Int {
	if len(data) == 0 {
		return big.NewInt(0)
	}
	n := new(big.Int).SetBytes(data)
	if data[0]&0x80 == 0 {
		return n
	}
	twoPow := new(big.Int).Lsh(big.NewInt(1), uint(len(data)*8))
	return n.Sub(n, twoPow)
}

func DecodeUnsignedBigInt(data []byte) *big.Int {
	return new(big.Int).SetBytes(data)
}

func DecodeOID(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}

	parts := []string{}
	first := int(data[0])
	switch {
	case first < 40:
		parts = append(parts, "0", fmt.Sprintf("%d", first))
	case first < 80:
		parts = append(parts, "1", fmt.Sprintf("%d", first-40))
	default:
		parts = append(parts, "2", fmt.Sprintf("%d", first-80))
	}

	value := 0
	for _, b := range data[1:] {
		value = (value << 7) | int(b&0x7F)
		if b&0x80 == 0 {
			parts = append(parts, fmt.Sprintf("%d", value))
			value = 0
		}
	}
	if value != 0 {
		return "", &Error{Msg: "truncated OID encoding"}
	}
	return join(parts, "."), nil
}

func DecodeValue(tlv TLV) (string, string, error) {
	class := DecodeTagClass(tlv.Tag)
	number := DecodeTagNumber(tlv.Tag)

	switch {
	case class == "universal" && number == 2:
		return "INTEGER", DecodeSignedBigInt(tlv.Value).String(), nil
	case class == "universal" && number == 4:
		if utf8.Valid(tlv.Value) {
			return "OCTET STRING", string(tlv.Value), nil
		}
		return "OCTET STRING", "0x" + hex.EncodeToString(tlv.Value), nil
	case class == "universal" && number == 5:
		return "NULL", "", nil
	case class == "universal" && number == 6:
		oid, err := DecodeOID(tlv.Value)
		return "OBJECT IDENTIFIER", oid, err
	case class == "universal" && number == 3:
		return "BIT STRING", "0x" + hex.EncodeToString(tlv.Value), nil
	case class == "universal" && number == 16:
		return "SEQUENCE", "0x" + hex.EncodeToString(tlv.Value), nil
	case class == "application" && number == 0:
		if len(tlv.Value) == net.IPv4len {
			return "IPADDRESS", net.IP(tlv.Value).String(), nil
		}
		if len(tlv.Value) == net.IPv6len {
			return "IPADDRESS", net.IP(tlv.Value).String(), nil
		}
		return "IPADDRESS", "0x" + hex.EncodeToString(tlv.Value), nil
	case class == "application" && number == 1:
		return "COUNTER32", DecodeUnsignedBigInt(tlv.Value).String(), nil
	case class == "application" && number == 2:
		return "GAUGE32", DecodeUnsignedBigInt(tlv.Value).String(), nil
	case class == "application" && number == 3:
		return "TIMETICKS", DecodeUnsignedBigInt(tlv.Value).String(), nil
	case class == "application" && number == 4:
		return "OPAQUE", "0x" + hex.EncodeToString(tlv.Value), nil
	case class == "application" && number == 6:
		return "COUNTER64", DecodeUnsignedBigInt(tlv.Value).String(), nil
	case class == "context" && number == 0:
		return "noSuchObject", "", nil
	case class == "context" && number == 1:
		return "noSuchInstance", "", nil
	case class == "context" && number == 2:
		return "endOfMibView", "", nil
	default:
		return fmt.Sprintf("%s[%d]", class, number), "0x" + hex.EncodeToString(tlv.Value), nil
	}
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}
