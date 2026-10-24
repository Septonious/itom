package transport

import (
	"crypto/sha256"

	"itom.local/core/journal"
	"itom.local/core/wire"
)

// Receive accepts a complete BODY from this actual trusted selected scope.
// It never performs FETCH itself. A server durably accounts the CLIENT body
// before Noise processing/confirmation; own writes follow original full-body
// observation. Physical proof and encrypted key confirmation remain separate.
func (b *SessionBootstrap) Receive(executor *Executor, physical Body) error {
	return b.receiveBody(executor, physical, nil)
}

func (b *SessionBootstrap) receiveBody(executor *Executor, physical Body, driver *BootstrapDriver) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.driver != driver {
		return ErrProtocol
	}
	if b.closed || b.failed || b.handedOff {
		return ErrClosed
	}
	if executor == nil || physical.UID == 0 || physical.Sequence == 0 || executor.account != b.origin.Account || executor.mailbox != b.origin.Folder || executor.validity != b.origin.UIDValidity {
		return ErrProtocol
	}
	envelope, err := openMailboxEnvelope(physical.Bytes, b.key[:])
	if err != nil {
		return err
	}
	defer clear(envelope.body)
	usage := envelope.usage
	if usage.Attempt == nil || usage.Key.Store != b.origin.Store || usage.Key.Epoch != b.origin.Run || usage.Key.Account != b.origin.Account || usage.Key.User != b.binding.User || usage.Key.Session != b.binding.Session || usage.Attempt.Folder != b.origin.Folder || envelope.class != 0 && envelope.class != wire.ServerControl {
		return ErrProtocol
	}
	own := (envelope.class == 0) == b.client
	if !b.client {
		observation := journal.Observation{Account: b.origin.Account, Folder: b.origin.Folder, EncodedBytes: envelope.charge, Location: journal.Location{UIDValidity: executor.validity, UID: physical.UID}, Decoded: envelope.body}
		if own {
			ref, err := b.store.LocalAttempt(usage.Key, usage.Attempt.Batch)
			if err != nil {
				return err
			}
			if err = b.store.Observe(ref, b.origin.Run, observation); err != nil {
				return err
			}
		} else {
			if _, _, err := b.store.AcceptRemoteBody(b.binding.User, b.origin.Run, usage, observation); err != nil {
				return err
			}
		}
	}
	f, err := decodeBootstrapFrame(envelope.packet)
	if err != nil {
		return err
	}
	if f.binding != b.binding || f.generation != b.generation || (f.kind&1 == 1) != (envelope.class == 0) {
		return ErrProtocol
	}
	if !b.knownUIDs[physical.UID] && len(b.knownUIDs) >= maxBootstrapUIDs {
		return ErrCapacity
	}
	digest := sha256.Sum256(envelope.packet)
	if own {
		if expected, ok := b.own[f.kind]; !ok || expected != digest {
			return ErrProtocol
		}
		b.observed[f.kind-1] = true
		b.knownUIDs[physical.UID] = true
		return nil
	}
	if previous, ok := b.peer[f.kind]; ok {
		if previous != digest {
			return ErrProtocol
		}
		b.knownUIDs[physical.UID] = true
		return nil
	}
	if f.kind != b.receive {
		return ErrProtocol
	}
	if err = b.receivePayloadLocked(f); err != nil {
		return err
	}
	b.peer[f.kind], b.knownUIDs[physical.UID] = digest, true
	b.observed[f.kind-1] = true
	return nil
}
