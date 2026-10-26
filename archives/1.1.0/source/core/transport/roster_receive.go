package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"itom.local/core/journal"
	"itom.local/core/wire"
)

// Observe and durably account the full original envelope before protocol
// processing. The AEAD validator is pure; wallet I/O happens after it releases
// the receive lock. No APPEND completion can stand in for a peer's confirmation.
func (d *RosterDriver) receiveBody(physical Body) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.failed {
		return ErrClosed
	}
	if physical.UID == 0 || physical.Sequence == 0 {
		return ErrProtocol
	}
	origin := d.input.origin
	binding := d.input.epoch.Context()
	envelope, err := openMailboxEnvelope(physical.Bytes, d.key[:])
	if err != nil {
		return err
	}
	defer clear(envelope.body)
	if old, err := d.anchor.history.historical(d.anchor.account, d.anchor.mailbox, envelope); err != nil || old {
		return err
	}
	usage := envelope.usage
	if usage.Attempt == nil || usage.Key.Store != origin.Store || usage.Key.Epoch != origin.Run || usage.Key.Account != origin.Account || usage.Key.User != binding.User || usage.Key.Session != binding.Session || usage.Attempt.Folder != origin.Folder || envelope.class != 0 && envelope.class != wire.ServerControl {
		return ErrProtocol
	}
	own := (envelope.class == 0) == d.client
	if !d.client {
		observation := journal.Observation{Account: origin.Account, Folder: origin.Folder, EncodedBytes: envelope.charge, Location: journal.Location{UIDValidity: d.anchor.validity, UID: physical.UID}, Decoded: envelope.body}
		if own {
			ref, err := d.store.LocalAttempt(usage.Key, usage.Attempt.Batch)
			if err != nil {
				return err
			}
			if err = d.store.Observe(ref, origin.Run, observation); err != nil {
				return err
			}
		} else {
			if _, _, err = d.store.AcceptRemoteBody(binding.User, origin.Run, usage, observation); err != nil {
				return err
			}
		}
	}
	if len(envelope.packet) < 4 || string(envelope.packet[:4]) != "IRO2" {
		return ErrProtocol
	}
	if !d.known[physical.UID] && len(d.known) >= maxBootstrapUIDs {
		return ErrCapacity
	}
	digest := sha256.Sum256(envelope.packet)
	if own {
		stage, ok := d.own[digest]
		if !ok {
			return ErrProtocol
		}
		d.ownSeen[digest] = true
		d.known[physical.UID] = true
		if stage == 1 {
			count := 0
			for digest, kind := range d.own {
				if kind == 1 && d.ownSeen[digest] {
					count++
				}
			}
			d.observed[0] = count == (len(d.encoded)+maxRosterChunk-1)/maxRosterChunk
		} else {
			d.observed[stage-1] = true
		}
		return nil
	}
	if d.peer[digest] {
		d.known[physical.UID] = true
		return nil
	}
	var stage uint8
	var manifest RosterManifest
	var candidate []byte
	var complete bool
	plains, err := d.input.epoch.OpenSessionBatch([][]byte{envelope.packet[4:]}, func(plains [][]byte) error {
		p := plains[0]
		if len(p) < 9 || binary.BigEndian.Uint64(p[1:9]) != d.input.generation {
			return ErrProtocol
		}
		stage = p[0]
		if stage < 1 || stage > 3 || stage != d.receive || (stage%2 == 1) != d.client {
			return ErrProtocol
		}
		if stage == 1 {
			var err error
			candidate, manifest, complete, err = d.validateOfferChunk(p[9:])
			if err != nil {
				return err
			}
		} else if !bytes.Equal(p[9:], d.digest[:]) {
			return ErrProtocol
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer clear(plains[0])
	if stage == 1 {
		raw := plains[0][9:]
		if !complete {
			if d.offerTotal == 0 {
				d.offerTotal = int(binary.BigEndian.Uint32(raw[4:8]))
				copy(d.offerDigest[:], raw[8:40])
			}
			if d.offerParts == nil {
				d.offerParts = map[int][]byte{}
			}
			d.offerParts[int(binary.BigEndian.Uint32(raw[:4]))] = append([]byte(nil), raw[40:]...)
			d.peer[digest], d.known[physical.UID] = true, true
			return nil
		}
		if err := d.openWallets(manifest); err != nil {
			d.failed = true
			clear(candidate)
			return err
		}
		d.manifest = manifest
		d.encoded = candidate
		for _, part := range d.offerParts {
			clear(part)
		}
		d.offerParts = nil
		d.digest = sha256.Sum256(d.encoded)
	}
	d.peer[digest], d.known[physical.UID] = true, true
	d.observed[stage-1] = true
	d.receive = 0
	if stage < 3 {
		d.send = stage + 1
	}
	return nil
}
