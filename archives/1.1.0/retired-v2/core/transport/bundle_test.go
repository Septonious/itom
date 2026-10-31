package transport_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"testing"
)

func rawBundle(packets ...[]byte) []byte {
	body := make([]byte, 8)
	copy(body, "ISB2")
	binary.BigEndian.PutUint16(body[4:6], uint16(len(packets)))
	for _, p := range packets {
		body = binary.BigEndian.AppendUint32(body, uint32(len(p)))
		body = append(body, p...)
	}
	return body
}
func TestFailedBatchAuthenticationDoesNotConsumeEarlierRecordReplayCounters(t *testing.T) {
	client, server := cipherPair(t, wire.ID{19})
	records := []transport.Record{incoming(transport.Data, 1, 0, []byte("first")), incoming(transport.Data, 3, 0, []byte("second"))}
	body, err := client.SealRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), body...)
	bad[len(bad)-1] ^= 1
	if _, err = server.OpenRecords(bad); !errors.Is(err, secure.ErrAuth) {
		t.Fatal("corrupted batch was accepted", err)
	}
	actual, err := server.OpenRecords(body)
	if err != nil || len(actual) != 2 || !bytes.Equal(actual[0].Payload, records[0].Payload) || !bytes.Equal(actual[1].Payload, records[1].Payload) {
		t.Fatal("earlier packet was poisoned by a later authentication failure", err)
	}
	if _, err = server.OpenRecords(body); !errors.Is(err, secure.ErrReplay) {
		t.Fatal("whole batch replay accepted", err)
	}
}
func TestInvalidRecordOrMixedLedgerInBatchDoesNotAdvanceAnyReplayCounters(t *testing.T) {
	for _, variant := range []string{"control", "generation", "duplicate"} {
		t.Run(variant, func(t *testing.T) {
			client, server := cipherPair(t, wire.ID{20})
			first, err := client.Seal(incoming(transport.Data, 1, 0, []byte("valid")))
			if err != nil {
				t.Fatal(err)
			}
			r := incoming(transport.Credit, 3, 64, nil)
			if variant == "generation" {
				r = incoming(transport.Data, 3, 0, []byte("wrong generation"))
				r.Generation = 8
			}
			second, err := client.Seal(r)
			if err != nil {
				t.Fatal(err)
			}
			if variant == "duplicate" {
				second = first
			}
			if _, err = server.OpenRecords(rawBundle(first, second)); err == nil {
				t.Fatal("invalid batch accepted")
			}
			if _, err = server.Open(first); err != nil {
				t.Fatal("rejected batch consumed a valid member", err)
			}
		})
	}
}
func TestMalformedContainerIsBoundedAndSingleRecordAPIRejectsContainers(t *testing.T) {
	client, server := cipherPair(t, wire.ID{21})
	body, err := client.SealRecords([]transport.Record{incoming(transport.Data, 1, 0, []byte("valid"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.Open(body); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("single API silently dropped batch members", err)
	}
	for _, bad := range [][]byte{body[:7], append(append([]byte(nil), body...), 0), append([]byte("ISB2\xff\xff\x00\x00"), body[8:]...)} {
		if _, err = server.OpenRecords(bad); err == nil {
			t.Fatal("malformed container accepted")
		}
	}
	if _, err = server.OpenRecords(body); err != nil {
		t.Fatal("framing rejection poisoned valid records", err)
	}
}
