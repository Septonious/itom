package transport

import "itom.local/core/wire"

// NewClientInbox binds the decoder to the actual selected account/mailbox and
// UIDVALIDITY, before allowing FETCH or any peer delivery.
func NewClientInbox(executor *Executor, limits InboxLimits, receivers ...*ClientReceiver) (*Inbox, error) {
	decode, err := clientInboxDecoder(receivers...)
	if err != nil {
		return nil, err
	}
	o := receivers[0].origin
	inbox, err := newScopedInbox(executor, receivers[0].session, InboxScope{Account: o.Account, Folder: o.Folder, UIDValidity: o.UIDValidity}, limits, decode)
	if err != nil {
		return nil, err
	}
	inbox.routes = make(map[LedgerClass]wire.ID, len(receivers))
	for _, route := range receivers {
		inbox.routes[route.class] = route.origin.Store
	}
	return inbox, nil
}

// NewClientInboxDecoder demultiplexes peer DATA/protocol under negotiated scope.
// Authenticated own CLIENT bodies are ignored, not decrypted, acknowledged,
// reconciled, retried or refunded. This path has no wallet/journal write API;
// old-session inventory and missing-wallet reconciliation remain separate.
// Direct decoder callers must supply a BODY from this exact trusted scope;
// NewClientInbox enforces the selected-executor binding automatically.
func NewClientInboxDecoder(receivers ...*ClientReceiver) (InboxDecoder, error) {
	decode, err := clientInboxDecoder(receivers...)
	if err != nil {
		return nil, err
	}
	return standaloneInboxDecoder(decode), nil
}

func clientInboxDecoder(receivers ...*ClientReceiver) (scopedInboxDecoder, error) {
	if len(receivers) < 1 || len(receivers) > 2 || receivers[0] == nil {
		return nil, ErrProtocol
	}
	first := receivers[0]
	routes := map[wire.ID]*ClientReceiver{}
	classes := map[LedgerClass]bool{}
	for _, r := range receivers {
		if r == nil || r.session != first.session || r.cipher != first.cipher || r.origin.Account != first.origin.Account || r.origin.User != first.origin.User || r.origin.Folder != first.origin.Folder || r.origin.UIDValidity != first.origin.UIDValidity || r.authenticationKey != first.authenticationKey || classes[r.class] || routes[r.origin.Store] != nil {
			return nil, ErrProtocol
		}
		routes[r.origin.Store] = r
		classes[r.class] = true
	}
	return func(validity uint32, physical Body, available inboxAdmission) (InboxDelivery, error) {
		if validity != first.origin.UIDValidity || physical.UID == 0 {
			return nil, ErrProtocol
		}
		envelope, err := openMailboxEnvelope(physical.Bytes, first.authenticationKey[:])
		if err != nil {
			return nil, err
		}
		defer clear(envelope.body)
		if old, err := first.history.historical(first.origin.Account, first.origin.Folder, envelope); err != nil || old {
			return nil, err
		}
		usage := envelope.usage
		r := routes[usage.Key.Store]
		if r == nil || usage.Attempt == nil || usage.Key.Epoch != r.origin.Run || usage.Key.Account != r.origin.Account || usage.Key.User != r.origin.User || usage.Key.Session != r.cipher.epoch.Context().Session || usage.Attempt.Folder != r.origin.Folder {
			return nil, ErrProtocol
		}
		if envelope.class == 0 {
			return nil, nil
		}
		expected := wire.ServerData
		if r.class == ControlLedger {
			expected = wire.ServerControl
		}
		if envelope.class != expected {
			return nil, ErrProtocol
		}
		admit, progressOnly, err := peerInboxAdmission(r.session, r.class, available)
		if err != nil {
			return nil, err
		}
		delivery, err := r.receiveAdmittedEnvelope(validity, physical.UID, envelope, admit)
		if delivery == nil {
			return nil, err
		}
		return classifyDelivery(delivery, r.class, progressOnly, err)
	}, nil
}
