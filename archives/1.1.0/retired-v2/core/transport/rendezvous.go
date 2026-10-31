package transport

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// A standing rendezvous is one normally charged server CONTROL body. A client
// can read it before it has any write grant. Its bounded permanent keyword is
// a MAC-authenticated request for a grant bound to that client's FRESH wallet
// Session, not a physical message or grant itself. No uncharged HELLO body,
// mailbox copy, new folder, quota reset or provider-marker refund is involved.
// Providers must actually advertise writable permanent keywords on SELECT.
func PrepareRendezvous(ctx context.Context, store *journal.Store, template InvitationTemplate, account, folder string, validity uint32, key []byte) (*PreparedPublication, error) {
	if store == nil || template.Generation == 0 || template.Binding.Session != (wire.ID{}) || template.Binding.Handshake != (wire.ID{}) || validity == 0 || len(key) != 32 {
		return nil, ErrProtocol
	}
	binding := template.Binding
	if _, err := rand.Read(binding.Session[:]); err != nil {
		return nil, err
	}
	if _, err := rand.Read(binding.Handshake[:]); err != nil {
		return nil, err
	}
	if _, err := secure.NewSessionHandshake(binding, key, false); err != nil {
		return nil, err
	}
	packet, err := encodeSessionFrame("ISR2", bootstrapFrame{binding: binding, generation: template.Generation, payload: []byte{1}})
	if err != nil {
		return nil, err
	}
	defer clear(packet)
	origin := ServerOrigin{Store: store.StoreID(), Run: store.Run(), Account: account, User: binding.User, Folder: folder, UIDValidity: validity}
	return prepareControlPacket(ctx, store, origin, binding.Session, packet, key)
}

type VerifiedRendezvous struct {
	mu       sync.Mutex
	template InvitationTemplate
	origin   ServerOrigin
	uid      uint32
	digest   [32]byte
	keyHash  [32]byte
	issuer   *journal.Store
	prepared map[wire.ID]invitationPreparation
}

type invitationPreparation struct {
	publication *PreparedPublication
	err         error
}

func verifyRendezvous(executor *Executor, physical Body, expected InvitationTemplate, key []byte) (*VerifiedRendezvous, mailboxEnvelope, error) {
	if executor == nil || executor.account == "" || executor.mailbox == "" || executor.validity == 0 || physical.UID == 0 || physical.Sequence == 0 || len(key) != 32 || expected.Binding.Session != (wire.ID{}) || expected.Binding.Handshake != (wire.ID{}) || expected.Generation == 0 {
		return nil, mailboxEnvelope{}, ErrProtocol
	}
	e, err := openMailboxEnvelope(physical.Bytes, key)
	if err != nil {
		return nil, e, err
	}
	f, err := decodeSessionFrame(e.packet, "ISR2")
	if err != nil {
		return nil, e, err
	}
	identity := f.binding
	identity.Session, identity.Handshake = wire.ID{}, wire.ID{}
	if f.kind != 0 || f.generation != expected.Generation || len(f.payload) != 1 || f.payload[0] != 1 || identity != expected.Binding || e.class != wire.ServerControl || e.usage.Attempt == nil || e.usage.Key.Account != executor.account || e.usage.Key.User != f.binding.User || e.usage.Key.Session != f.binding.Session || e.usage.Attempt.Folder != executor.mailbox {
		return nil, e, ErrProtocol
	}
	if _, err := secure.NewSessionHandshake(f.binding, key, true); err != nil {
		return nil, e, err
	}
	v := &VerifiedRendezvous{template: expected, origin: ServerOrigin{Store: e.usage.Key.Store, Run: e.usage.Key.Epoch, Account: executor.account, User: f.binding.User, Folder: executor.mailbox, UIDValidity: executor.validity}, uid: physical.UID, digest: sha256.Sum256(e.body), keyHash: sha256.Sum256(key)}
	return v, e, nil
}

func VerifyRendezvous(executor *Executor, physical Body, expected InvitationTemplate, key []byte) (*VerifiedRendezvous, error) {
	v, e, err := verifyRendezvous(executor, physical, expected, key)
	clear(e.body)
	return v, err
}

func ObserveRendezvous(store *journal.Store, executor *Executor, physical Body, expected InvitationTemplate, key []byte) (*VerifiedRendezvous, error) {
	v, e, err := verifyRendezvous(executor, physical, expected, key)
	defer clear(e.body)
	if err != nil {
		return nil, err
	}
	if store == nil || store.StoreID() != v.origin.Store || store.Run() != v.origin.Run {
		return nil, ErrProtocol
	}
	ref, err := store.LocalAttempt(e.usage.Key, e.usage.Attempt.Batch)
	if err != nil {
		return nil, err
	}
	if err = store.Observe(ref, store.Run(), journal.Observation{Account: v.origin.Account, Folder: v.origin.Folder, EncodedBytes: e.charge, Location: journal.Location{UIDValidity: executor.validity, UID: physical.UID}, Decoded: e.body}); err != nil {
		return nil, err
	}
	v.issuer = store
	return v, nil
}

func (v *VerifiedRendezvous) OpenWallet(path string) (*wallet.Wallet, error) {
	if v == nil {
		return nil, ErrProtocol
	}
	return wallet.Open(path, v.origin.User, v.origin.Store)
}

func (v *VerifiedRendezvous) InvitationFor(w *wallet.Wallet) (InvitationTemplate, error) {
	if v == nil || w == nil {
		return InvitationTemplate{}, ErrProtocol
	}
	identity := w.Identity()
	if identity.Store != v.origin.Store || identity.User != v.origin.User {
		return InvitationTemplate{}, ErrProtocol
	}
	template := v.template
	template.Binding.Session = identity.Session
	return template, nil
}

func (v *VerifiedRendezvous) VerifyInvitation(executor *Executor, physical Body, w *wallet.Wallet, key []byte) (*VerifiedInvitation, error) {
	template, err := v.InvitationFor(w)
	if err != nil {
		return nil, err
	}
	offer, err := VerifySessionInvitation(executor, physical, template, key)
	if err != nil {
		return nil, err
	}
	if offer.origin != v.origin || offer.uid == v.uid {
		return nil, ErrProtocol
	}
	offer.anchorUID = v.uid
	return offer, nil
}

func (v *VerifiedRendezvous) ObserveInvitation(executor *Executor, physical Body, keyword string, key []byte) (*VerifiedInvitation, error) {
	template, err := v.RequestTemplate(keyword, key)
	if err != nil {
		return nil, err
	}
	offer, err := ObserveSessionInvitation(v.issuer, executor, physical, template, key)
	if err != nil {
		return nil, err
	}
	if offer.origin != v.origin || offer.uid == v.uid {
		return nil, ErrProtocol
	}
	offer.anchorUID = v.uid
	return offer, nil
}

func (v *VerifiedRendezvous) requestMAC(session wire.ID, key []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("ITOM/session-v2/rendezvous-request\x00"))
	h.Write(v.digest[:])
	h.Write(session[:])
	var location [8]byte
	binary.BigEndian.PutUint32(location[:4], v.origin.UIDValidity)
	binary.BigEndian.PutUint32(location[4:], v.uid)
	h.Write(location[:])
	return h.Sum(nil)
}

// RequestKeyword is bounded metadata, never an APPEND or quota permit. The
// authority verifies its MAC and admits any resulting offer durably. Keeping
// it separate from submission permits safe idempotent STORE after a lost tag;
// repeating the same flag does not create another physical body or nonce.
func (v *VerifiedRendezvous) RequestKeyword(w *wallet.Wallet, key []byte) (string, error) {
	if v == nil || w == nil || len(key) != 32 || sha256.Sum256(key) != v.keyHash {
		return "", ErrProtocol
	}
	identity := w.Identity()
	if identity.Store != v.origin.Store || identity.User != v.origin.User {
		return "", ErrProtocol
	}
	return "ITOM2_" + hex.EncodeToString(identity.Session[:]) + "_" + hex.EncodeToString(v.requestMAC(identity.Session, key)), nil
}

// RequestTemplate accepts only a profile-key MAC bound to this issuer's actual
// full observed anchor/body/location/run. A provider keyword on its own can
// never reserve storage or reset an original session's counters.
func (v *VerifiedRendezvous) RequestTemplate(keyword string, key []byte) (InvitationTemplate, error) {
	if v == nil || v.issuer == nil || v.issuer.StoreID() != v.origin.Store || v.issuer.Run() != v.origin.Run || len(key) != 32 || sha256.Sum256(key) != v.keyHash || len(keyword) != 103 || !validKeyword(keyword) || !strings.EqualFold(keyword[:6], "ITOM2_") || keyword[38] != '_' {
		return InvitationTemplate{}, ErrProtocol
	}
	rawSession, err := hex.DecodeString(keyword[6:38])
	if err != nil {
		return InvitationTemplate{}, ErrProtocol
	}
	rawMAC, err := hex.DecodeString(keyword[39:])
	if err != nil {
		return InvitationTemplate{}, ErrProtocol
	}
	var session wire.ID
	copy(session[:], rawSession)
	if session == (wire.ID{}) || !hmac.Equal(v.requestMAC(session, key), rawMAC) {
		return InvitationTemplate{}, ErrProtocol
	}
	template := v.template
	template.Binding.Session = session
	return template, nil
}

func (v *VerifiedRendezvous) Submit(ctx context.Context, executor *Executor, w *wallet.Wallet, key []byte) error {
	return v.submit(ctx, executor, w, key, nil)
}

func (v *VerifiedRendezvous) submit(ctx context.Context, executor *Executor, w *wallet.Wallet, key []byte, stage func(string)) error {
	_, err := v.submitTracked(ctx, executor, w, key, stage)
	return err
}

func (v *VerifiedRendezvous) submitTracked(ctx context.Context, executor *Executor, w *wallet.Wallet, key []byte, stage func(string)) ([]string, error) {
	if v == nil || executor == nil || executor.account != v.origin.Account || executor.mailbox != v.origin.Folder || executor.validity != v.origin.UIDValidity {
		return nil, ErrProtocol
	}
	keyword, err := v.RequestKeyword(w, key)
	if err != nil {
		return nil, err
	}
	// Providers advertising permanent keywords can still reject long names.
	// Only an explicit tagged refusal permits another metadata representation;
	// a timeout or unknown outcome never triggers a physical publication retry.
	forms := [][]string{{keyword}}
	for _, width := range []int{48, 20, 5} {
		forms = append(forms, requestFragments(keyword, width))
	}
	for index, flags := range forms {
		if len(flags) == 0 {
			return nil, ErrProtocol
		}
		if stage != nil {
			if index == 0 {
				stage("request")
			} else {
				stage(fmt.Sprintf("request_%d", len(flags[0])))
			}
		}
		command, e := executor.storeKeywords(ctx, v.uid, flags, true)
		if e == nil {
			_, e = command.Wait(ctx)
		}
		if !errors.Is(e, ErrRefused) {
			if e == nil {
				executor.rememberKeywordBytes(len(flags[0]))
			}
			return flags, e
		}
		err = e
	}
	return nil, err
}

// PrepareRequestedInvitation retains one exact result for each fresh Session.
// Repeated flags return the original pending body/error, never another spend.
// A failed preparation remains conservatively fenced for explicit recovery.
// A second observer is also fenced by the issuer's atomic initial-session gate.
func (v *VerifiedRendezvous) PrepareRequestedInvitation(ctx context.Context, keyword string, allowance pool.Amount, key []byte) (*PreparedPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	template, err := v.RequestTemplate(keyword, key)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if prior, ok := v.prepared[template.Binding.Session]; ok {
		return prior.publication, prior.err
	}
	if len(v.prepared) >= v.issuer.Config().MaxGrants {
		return nil, ErrCapacity
	}
	p, err := PrepareSessionInvitation(ctx, v.issuer, template, v.origin.Account, v.origin.Folder, v.origin.UIDValidity, allowance, key)
	if v.prepared == nil {
		v.prepared = make(map[wire.ID]invitationPreparation)
	}
	v.prepared[template.Binding.Session] = invitationPreparation{p, err}
	return p, err
}
