package wire

import "testing"

func batchPacket(t *testing.T, counter uint64) []byte {
	t.Helper()
	f := Frame{Type: Data, Stream: 1 + 2*counter, Logical: counter + 1, Offset: 0, Payload: []byte{1, 2, 3}}
	inner, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	h := Header{Kind: Record, Pair: ID{1}, Session: ID{2}, Epoch: 1, Direction: C2S, Counter: counter, BodyLength: uint32(HeaderSize + len(inner) + TagSize)}
	head, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	// Parser only validates the outer encrypted envelope length. The test body
	// deliberately uses zero ciphertext bytes because authentication belongs to
	// secure.Epoch, not the batch framing layer.
	return append(head, make([]byte, h.BodyLength)...)
}

func checkDataBatchRoundTrip(t *testing.T) {
	packets := [][]byte{batchPacket(t, 1), batchPacket(t, 2), batchPacket(t, 3)}
	body, err := PackDataBatch(ID{9}, packets)
	if err != nil {
		t.Fatal(err)
	}
	id, got, err := UnpackDataBatch(body)
	if err != nil || id != (ID{9}) || len(got) != len(packets) {
		t.Fatal(id, len(got), err)
	}
	for i := range got {
		if string(got[i]) != string(packets[i]) {
			t.Fatalf("packet %d changed", i)
		}
	}
}

func checkDataBatchRejectsMixedContextAndTrailingBytes(t *testing.T) {
	a, b := batchPacket(t, 1), batchPacket(t, 2)
	b[24] ^= 1 // session ID byte
	if _, err := PackDataBatch(ID{9}, [][]byte{a, b}); err == nil {
		t.Fatal("mixed session accepted")
	}
	body, err := PackDataBatch(ID{9}, [][]byte{a})
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, 1)
	if _, _, err = UnpackDataBatch(body); err == nil {
		t.Fatal("trailing bytes accepted")
	}
}

func checkDataBatchBodyLimitExceedsSingleRecord(t *testing.T) {
	if MaxDataBatchBody <= MaxRecord {
		t.Fatalf("batch body cap %d must exceed single-record cap %d", MaxDataBatchBody, MaxRecord)
	}
	if MaxDataBatchBody != 24+MaxDataBatchRecords*4+MaxDataBatchPayload {
		t.Fatal("batch body cap drifted from framing limits")
	}
}

func checkPhysicalMultiRecordBatchExceedsLegacyGateButFitsS1Gate(t *testing.T) {
	packets := make([][]byte, 0, 4)
	for counter := uint64(1); counter <= 4; counter++ {
		h := Header{Kind: Record, Pair: ID{1}, Session: ID{2}, Epoch: 1, Direction: C2S, Counter: counter, BodyLength: MaxBody}
		head, err := h.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, append(head, make([]byte, MaxBody)...))
	}
	batch, err := PackDataBatch(ID{9}, packets)
	if err != nil {
		t.Fatal(err)
	}
	key := QuotaKey{Store: ID{3}, Epoch: 1, Grant: 1, Session: ID{2}, User: "12345678", Account: "a01"}
	physical, err := BindPhysical(key, 1, ID{7}, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(physical) <= MaxRecord+256 {
		t.Fatalf("test batch %d did not cross legacy single-record gate %d", len(physical), MaxRecord+256)
	}
	if len(physical) > MaxDataBatchBody+256 {
		t.Fatalf("valid S1 physical batch %d exceeds bounded admission %d", len(physical), MaxDataBatchBody+256)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioDataBatch(t *testing.T) {
	t.Log("check=checkDataBatchRoundTrip")
	checkDataBatchRoundTrip(t)
	t.Log("check=checkDataBatchRejectsMixedContextAndTrailingBytes")
	checkDataBatchRejectsMixedContextAndTrailingBytes(t)
	t.Log("check=checkDataBatchBodyLimitExceedsSingleRecord")
	checkDataBatchBodyLimitExceedsSingleRecord(t)
	t.Log("check=checkPhysicalMultiRecordBatchExceedsLegacyGateButFitsS1Gate")
	checkPhysicalMultiRecordBatchExceedsLegacyGateButFitsS1Gate(t)
}
