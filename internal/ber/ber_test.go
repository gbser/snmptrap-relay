package ber

import "testing"

func TestReadTLVRejectsOversizedLongFormLength(t *testing.T) {
	r := NewReader([]byte{0x30, 0x89, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})

	if _, err := r.ReadTLV(); err == nil {
		t.Fatal("ReadTLV() error = nil, want error")
	}
}

func TestReadTLVRejectsLengthOverflow(t *testing.T) {
	r := NewReader([]byte{0x30, 0x84, 0xff, 0xff, 0xff, 0xff})

	if _, err := r.ReadTLV(); err == nil {
		t.Fatal("ReadTLV() error = nil, want error")
	}
}
