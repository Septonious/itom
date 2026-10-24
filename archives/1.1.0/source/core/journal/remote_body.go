package journal

import (
	"crypto/sha256"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

// AcceptRemoteBody is called by the trusted scoped IMAP BODY worker after
// authenticating the physical envelope. It atomically registers the contiguous
// descriptor and observes the actual owned BODY/UID. There is one durable
// commit, no ACK-before-fsync and no release/refund operation in this method.
// In particular, failed evidence cannot leave a newly registered descriptor.
func (s *Store) AcceptRemoteBody(user string, run uint64, usage wire.UsageReport, evidence Observation) (wire.CleanupStatus, AttemptRef, error) {
	if usage.Attempt == nil || user != usage.Key.User {
		return wire.CleanupStatus{}, AttemptRef{}, ErrScope
	}
	if _, err := wire.EncodeControl(0, usage); err != nil {
		return wire.CleanupStatus{}, AttemptRef{}, ErrScope
	}
	if len(evidence.Decoded) == 0 || len(evidence.Decoded) > MaxDecoded {
		return wire.CleanupStatus{}, AttemptRef{}, ErrLimit
	}
	digest := sha256.Sum256(evidence.Decoded)
	var status wire.CleanupStatus
	var ref AttemptRef
	err := s.write(func(tx *bolt.Tx) error {
		if run != s.run {
			return ErrGeneration
		}
		g, err := s.grant(tx, refFromKey(usage.Key))
		if err != nil {
			return err
		}
		if !g.Remote || g.Run != usage.Key.Epoch {
			return ErrScope
		}
		if err = s.registerRemote(tx, &g, amount(usage.Total), *usage.Attempt); err != nil {
			return err
		}
		if err = s.saveGrant(tx, g); err != nil {
			return err
		}
		ref, err = s.remoteAttemptTx(tx, usage.Key, usage.Total.Messages)
		if err != nil {
			return err
		}
		proofs := []ObservedAttempt{{Ref: ref, Observation: evidence}}
		if err = s.observeManyTx(tx, run, proofs, [][32]byte{digest}); err != nil {
			return err
		}
		status = cleanupStatus(g)
		return nil
	})
	if err != nil {
		return wire.CleanupStatus{}, AttemptRef{}, err
	}
	return status, ref, nil
}
