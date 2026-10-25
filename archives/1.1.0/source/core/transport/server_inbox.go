package transport

import (
	"itom.local/core/journal"
	"itom.local/core/wire"
)

// NewServerInbox couples selected physical scope with all peer/own routes.
// A wrong account, folder or incarnation rejects before any mailbox command.
func NewServerInbox(executor *Executor, limits InboxLimits, receivers ...*RemoteReceiver) (*Inbox, error) {
	var inbox *Inbox
	decode, err := serverInboxDecoderWithReclamation(func(r *reclaimBody) { inbox.queueReclaimToken(r.location.UID, r) }, receivers...)
	if err != nil {
		return nil, err
	}
	r := receivers[0]
	inbox, err = newScopedInbox(executor, r.session, InboxScope{Account: r.account, Folder: r.folder, UIDValidity: r.validity}, limits, decode)
	if err != nil {
		return nil, err
	}
	inbox.server = true
	inbox.routes = make(map[LedgerClass]wire.ID, len(receivers))
	for _, route := range receivers {
		inbox.routes[route.class] = route.store.StoreID()
	}
	return inbox, nil
}

// NewServerInboxDecoder pins the authenticated peer/session and physical
// account/folder/incarnation before any delivery or local observation. DATA and
// protocol stores remain separate. Own SERVER bodies use the durable original
// attempt index and full observation proof; they never enter peer decryption.
// Other sessions/owners and malformed bodies fail closed, not as new traffic.
// Direct decoder callers must supply actual BODY evidence from this scope;
// NewServerInbox enforces the selected-executor binding automatically.
func NewServerInboxDecoder(receivers ...*RemoteReceiver) (InboxDecoder, error) {
	decode, err := serverInboxDecoder(receivers...)
	if err != nil {
		return nil, err
	}
	return standaloneInboxDecoder(decode), nil
}

func serverInboxDecoder(receivers ...*RemoteReceiver) (scopedInboxDecoder, error) {
	return serverInboxDecoderWithReclamation(nil, receivers...)
}

func serverInboxDecoderWithReclamation(reclaim func(*reclaimBody), receivers ...*RemoteReceiver) (scopedInboxDecoder, error) {
	if len(receivers) < 1 || len(receivers) > 2 || receivers[0] == nil {
		return nil, ErrProtocol
	}
	first := receivers[0]
	routes := map[wire.ID]*RemoteReceiver{}
	classes := map[LedgerClass]bool{}
	for _, r := range receivers {
		if r == nil || r.session != first.session || r.cipher != first.cipher || r.account != first.account || r.user != first.user || r.folder != first.folder || r.validity != first.validity || r.authenticationKey != first.authenticationKey || classes[r.class] || routes[r.store.StoreID()] != nil {
			return nil, ErrProtocol
		}
		routes[r.store.StoreID()] = r
		classes[r.class] = true
	}
	return func(validity uint32, physical Body, available inboxAdmission) (InboxDelivery, error) {
		if validity != first.validity || physical.UID == 0 {
			return nil, ErrProtocol
		}
		envelope, err := openMailboxEnvelope(physical.Bytes, first.authenticationKey[:])
		if err != nil {
			return nil, err
		}
		defer clear(envelope.body)
		usage := envelope.usage
		r := routes[usage.Key.Store]
		if r == nil || usage.Attempt == nil || usage.Key.Account != r.account || usage.Key.User != r.user || usage.Key.Session != r.cipher.epoch.Context().Session || usage.Attempt.Folder != r.folder {
			return nil, ErrProtocol
		}
		if envelope.class == 0 {
			admit, progressOnly, err := peerInboxAdmission(r.session, r.class, available)
			if err != nil {
				return nil, err
			}
			delivery, err := r.receiveAdmittedEnvelope(validity, physical.UID, envelope, admit)
			if delivery == nil {
				return nil, err
			}
			return classifyDelivery(delivery, r.class, progressOnly, err)
		}
		expected := wire.ServerData
		if r.class == ControlLedger {
			expected = wire.ServerControl
		}
		if envelope.class != expected {
			return nil, ErrProtocol
		}
		ref, err := r.store.LocalAttempt(usage.Key, usage.Attempt.Batch)
		if err != nil {
			return nil, err
		}
		// Correlation is not ownership proof. The original journal validates the
		// actual complete stored BODY against the persisted hash/size/folder.
		err = r.store.Observe(ref, r.store.Run(), journal.Observation{Account: r.account, Folder: r.folder, EncodedBytes: envelope.charge, Location: journal.Location{UIDValidity: validity, UID: physical.UID}, Decoded: envelope.body})
		if err == nil && reclaim != nil {
			reclaim(&reclaimBody{store: r.store, ref: ref, location: journal.Location{UIDValidity: validity, UID: physical.UID}, account: r.account, folder: r.folder, receiptKeyword: receiptKeyword(envelope.body, validity, physical.UID, r.authenticationKey[:])})
		}
		return nil, err
	}, nil
}
