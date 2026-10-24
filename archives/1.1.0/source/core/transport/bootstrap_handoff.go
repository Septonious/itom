package transport

import "itom.local/core/secure"

// Epoch exposes the confirmed secure counter authority needed by the original
// wallet's OpenBound API. It exposes no raw keys or mutable Noise cipher state.
func (b *SessionBootstrap) Epoch() (*secure.Epoch, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.confirmedLocked(); err != nil {
		return nil, err
	}
	return b.epoch, nil
}

// Inbox runs bounded actual-UID discovery before a Session/runtime exists. Its
// delivery action enforces bootstrap control accounting, not application DATA
// permission. The startup owner must stop it before handing the executor over.
func (b *SessionBootstrap) Inbox(executor *Executor, limits InboxLimits) (*Inbox, error) {
	if executor == nil || executor.account != b.origin.Account || executor.mailbox != b.origin.Folder || executor.validity != b.origin.UIDValidity {
		return nil, ErrProtocol
	}
	return NewInbox(executor, InboxScope{Account: b.origin.Account, Folder: b.origin.Folder, UIDValidity: b.origin.UIDValidity}, limits, func(_ uint32, body Body) (InboxDelivery, error) {
		return InboxAction(func() error { return b.Receive(executor, body) }), nil
	})
}

// BootstrapHandoff retains only scoped authenticated bootstrap UID knowledge.
// It conveys no deletion/refund authority, grant, payload or secret keys. Fields
// are private so only completed key establishment can mint a handoff.
type BootstrapHandoff struct {
	epoch      *secure.Epoch
	generation uint64
	client     bool
	origin     ServerOrigin
	uids       []uint32
}

// Handoff also requires actual BODY observation of all six startup messages,
// including this endpoint's last write. APPEND acceptance alone supplies no
// trustworthy UID history for the runtime's application decoder to skip.
func (b *SessionBootstrap) Handoff() (*BootstrapHandoff, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.confirmedLocked(); err != nil {
		return nil, err
	}
	for _, seen := range b.observed {
		if !seen {
			return nil, ErrBootstrapPending
		}
	}
	h := &BootstrapHandoff{epoch: b.epoch, generation: b.generation, client: b.client, origin: b.origin}
	for uid := range b.knownUIDs {
		h.uids = append(h.uids, uid)
	}
	b.handedOff = true
	return h, nil
}

func NewClientInboxWithBootstrap(executor *Executor, limits InboxLimits, handoff *BootstrapHandoff, receivers ...*ClientReceiver) (*Inbox, error) {
	if handoff == nil || !handoff.client || len(receivers) == 0 || receivers[0] == nil || receivers[0].cipher == nil || receivers[0].cipher.epoch != handoff.epoch {
		return nil, ErrProtocol
	}
	i, err := NewClientInbox(executor, limits, receivers...)
	if err != nil {
		return nil, err
	}
	for _, r := range receivers {
		if r.class == ControlLedger && r.origin != handoff.origin {
			return nil, ErrProtocol
		}
	}
	return i, i.adoptBootstrap(handoff)
}

func NewServerInboxWithBootstrap(executor *Executor, limits InboxLimits, handoff *BootstrapHandoff, receivers ...*RemoteReceiver) (*Inbox, error) {
	if handoff == nil || handoff.client || len(receivers) == 0 || receivers[0] == nil || receivers[0].cipher == nil || receivers[0].cipher.epoch != handoff.epoch {
		return nil, ErrProtocol
	}
	i, err := NewServerInbox(executor, limits, receivers...)
	if err != nil {
		return nil, err
	}
	for _, r := range receivers {
		if r.class == ControlLedger && r.store.Run() != handoff.origin.Run {
			return nil, ErrProtocol
		}
	}
	return i, i.adoptBootstrap(handoff)
}

func (i *Inbox) adoptBootstrap(h *BootstrapHandoff) error {
	if i.session.generation != h.generation || i.scope != (InboxScope{Account: h.origin.Account, Folder: h.origin.Folder, UIDValidity: h.origin.UIDValidity}) || i.routes[ControlLedger] != h.origin.Store || len(h.uids) > i.limits.KnownUIDs {
		return ErrProtocol
	}
	i.bootstrapUIDs = make(map[uint32]bool, len(h.uids))
	for _, uid := range h.uids {
		if uid == 0 {
			return ErrProtocol
		}
		i.seen[uid], i.bootstrapUIDs[uid] = true, true
	}
	return nil
}
