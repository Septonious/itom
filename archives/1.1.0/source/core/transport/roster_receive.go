package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"itom.local/core/journal"
	"itom.local/core/wallet"
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
		for stage, expected := range d.own {
			if digest == expected {
				d.observed[stage-1] = true
				d.known[physical.UID] = true
				return nil
			}
		}
		return ErrProtocol
	}
	for _, previous := range d.peer {
		if previous == digest {
			d.known[physical.UID] = true
			return nil
		}
	}
	var stage uint8
	var manifest RosterManifest
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
			manifest, err = decodeRoster(p[9:], d.plan, d.planHash)
			if err != nil {
				return err
			}
			if manifest.ControlStore != origin.Store || manifest.ControlRun != origin.Run {
				return ErrProtocol
			}
			// Both sides selected these exact physical incarnations, not merely names.
			for n, a := range manifest.Accounts {
				if a.FastUIDValidity != d.lanes[2*n].Executor.validity || a.BulkUIDValidity != d.lanes[2*n+1].Executor.validity {
					return ErrProtocol
				}
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
		w, err := wallet.OpenBound(d.dataPath, manifest.DataStore, d.input.epoch)
		if err != nil {
			d.failed = true
			return err
		}
		d.dataWallet = w
		d.manifest = manifest
		d.encoded = append([]byte(nil), plains[0][9:]...)
		d.digest = sha256.Sum256(d.encoded)
	}
	d.peer[stage], d.known[physical.UID] = digest, true
	d.observed[stage-1] = true
	d.receive = 0
	if stage < 3 {
		d.send = stage + 1
	}
	return nil
}
