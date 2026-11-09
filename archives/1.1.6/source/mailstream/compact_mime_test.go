package mailstream

import (
	"bytes"
	"testing"
)

func TestCompactMIMEEnvelopeRC116Compatibility(t *testing.T) {
	var sid SessionID
	sid[0]=1
	packet:=append(header(sealed,sid,5),[]byte("encrypted-control")...)
	current:=encodeMail(packet)
	if !bytes.Contains(current,[]byte("From: t@itom.invalid\r\nTo: t@itom.invalid\r\n")) {
		t.Fatal("short mail headers are absent")
	}
	previous:=bytes.Replace(current,[]byte("From: t@itom.invalid\r\nTo: t@itom.invalid\r\n"),[]byte("From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\n"),1)
	if saved:=len(previous)-len(current);saved!=16 {
		t.Fatalf("shorten per-APPEND overhead by exactly 16 bytes, got %d",saved)
	}
	for _,wire:=range [][]byte{current,previous} {
		packets,err:=decodeMail(wire)
		if err!=nil || len(packets)!=1 || !bytes.Equal(packets[0],packet) {
			t.Fatalf("MIME compatibility: packets=%d err=%v",len(packets),err)
		}
	}
}
