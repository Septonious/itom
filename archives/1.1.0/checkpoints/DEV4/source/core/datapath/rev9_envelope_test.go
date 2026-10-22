package datapath

import (
	"bytes"
	"crypto/sha256"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"testing"
)

func TestRev9EnvelopeVerifiedOnceStillRejectsCrossAccountTamperingAndReplay(t *testing.T) {
	for _, mode := range []string{"valid", "digest", "account", "aead"} {
		t.Run(mode, func(t *testing.T) {
			_, _, _, legacy, old, rx := field68BatchFixture(t)
			prefix, err := wire.BindPhysical(old.Key, 1, old.Attempt.Batch, []byte{0})
			if err != nil {
				t.Fatal(err)
			}
			packed := legacy[len(prefix)-1:]
			report, body, err := wire.BindDataEnvelope(old.Key, 1, old.Attempt.Batch, 0, old.Attempt.Folder, packed, bytes.Repeat([]byte{7}, 32), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "digest":
				body[len(body)-40] ^= 1
			case "account":
				report.Key.Account = "a02"
			case "aead":
				body[len(body)-40] ^= 1
				report.Attempt.Digest = sha256.Sum256(body)
			}
			frames, err := OpenRemoteBatchBody(body, report.Attempt.EncodedBytes, report, rx)
			if mode != "valid" {
				if err == nil || len(frames) != 0 {
					t.Fatal("invalid envelope delivered", mode)
				}
				return
			}
			if err != nil || len(frames) != 3 {
				t.Fatal(len(frames), err)
			}
			for _, f := range frames {
				clear(f.Payload)
			}
			if again, err := OpenRemoteBatchBody(body, report.Attempt.EncodedBytes, report, rx); err == nil || len(again) != 0 {
				t.Fatal("replayed ciphertext delivered")
			}
		})
	}
}
