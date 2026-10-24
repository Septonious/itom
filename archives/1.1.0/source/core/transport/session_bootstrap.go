package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"

	"itom.local/core/journal"
	"itom.local/core/secure"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

var ErrBootstrapPending = errors.New("V2_BOOTSTRAP_PENDING")

const bootstrapHeader = 128

// NNpsk0 with empty payload is DH25519 public key (32) plus AEAD tag (16).
// Length is checked against the actual Noise output before any writer permit.
const bootstrapNoiseBytes = 48
const bootstrapProofBytes = 33
const maxBootstrapUIDs = 16

type bootstrapKind uint8

const (
	bootstrapRequest bootstrapKind = iota + 1
	bootstrapResponse
	bootstrapClientProof
	bootstrapServerProof
	bootstrapClientAck
	bootstrapServerAck
)

type bootstrapFrame struct {
	kind       bootstrapKind
	binding    secure.Binding
	generation uint64
	payload    []byte
}

func bootstrapPayloadSize(kind bootstrapKind) int {
	if kind == bootstrapRequest || kind == bootstrapResponse {
		return bootstrapNoiseBytes
	}
	if kind >= bootstrapClientProof && kind <= bootstrapServerAck {
		n, _ := secure.SessionPacketSize(bootstrapProofBytes)
		return n
	}
	return 0
}

func (f bootstrapFrame) encode() ([]byte, error) {
	if f.generation == 0 || len(f.binding.User) != 8 || len(f.payload) != bootstrapPayloadSize(f.kind) || len(f.payload) == 0 {
		return nil, ErrProtocol
	}
	return encodeSessionFrame("ISH2", f)
}

func encodeSessionFrame(magic string, f bootstrapFrame) ([]byte, error) {
	if len(magic) != 4 || f.generation == 0 || len(f.binding.User) != 8 || len(f.payload) == 0 || len(f.payload) > wire.MaxHandshake {
		return nil, ErrProtocol
	}
	b := make([]byte, bootstrapHeader+len(f.payload))
	copy(b, magic)
	b[4], b[5] = 2, byte(f.kind)
	copy(b[8:24], f.binding.Server[:])
	copy(b[24:32], f.binding.User)
	copy(b[32:48], f.binding.Pair[:])
	copy(b[48:64], f.binding.Session[:])
	copy(b[64:80], f.binding.Handshake[:])
	binary.BigEndian.PutUint32(b[80:84], f.binding.Epoch)
	copy(b[84:116], f.binding.ConfigHash[:])
	binary.BigEndian.PutUint64(b[116:124], f.generation)
	binary.BigEndian.PutUint32(b[124:128], uint32(len(f.payload)))
	copy(b[bootstrapHeader:], f.payload)
	return b, nil
}

func decodeBootstrapFrame(raw []byte) (bootstrapFrame, error) {
	f, err := decodeSessionFrame(raw, "ISH2")
	if err != nil {
		return f, err
	}
	if len(f.payload) != bootstrapPayloadSize(f.kind) || bootstrapPayloadSize(f.kind) == 0 {
		return bootstrapFrame{}, ErrProtocol
	}
	return f, nil
}

func decodeSessionFrame(raw []byte, magic string) (bootstrapFrame, error) {
	var f bootstrapFrame
	if len(magic) != 4 || len(raw) <= bootstrapHeader || len(raw) > bootstrapHeader+wire.MaxHandshake || string(raw[:4]) != magic || raw[4] != 2 || raw[6] != 0 || raw[7] != 0 || int(binary.BigEndian.Uint32(raw[124:128])) != len(raw)-bootstrapHeader {
		return f, ErrProtocol
	}
	f.kind = bootstrapKind(raw[5])
	copy(f.binding.Server[:], raw[8:24])
	f.binding.User = string(raw[24:32])
	copy(f.binding.Pair[:], raw[32:48])
	copy(f.binding.Session[:], raw[48:64])
	copy(f.binding.Handshake[:], raw[64:80])
	f.binding.Epoch = binary.BigEndian.Uint32(raw[80:84])
	copy(f.binding.ConfigHash[:], raw[84:116])
	f.generation = binary.BigEndian.Uint64(raw[116:124])
	f.payload = raw[bootstrapHeader:]
	if f.generation == 0 {
		return bootstrapFrame{}, ErrProtocol
	}
	return f, nil
}

// SessionBootstrap establishes/explicitly confirms new Noise keys through
// physically accounted control mail. It does NOT issue the initial control
// grant, negotiate the full carrier roster, or authorize VPN CONNECTED.
// The production bootstrap-permission/invitation authority remains required.
// Methods serialize the bounded bootstrap actor; no Session lock or target I/O
// is held during its original wallet/journal transactions.
type SessionBootstrap struct {
	mu                                  sync.Mutex
	binding                             secure.Binding
	generation                          uint64
	client                              bool
	key                                 [32]byte
	handshake                           *secure.Handshake
	epoch                               *secure.Epoch
	cipher                              *Cipher
	wallet                              *wallet.Wallet
	store                               *journal.Store
	origin                              ServerOrigin
	send, receive                       bootstrapKind
	request, response                   []byte
	transcript                          [32]byte
	peer                                map[bootstrapKind][32]byte
	own                                 map[bootstrapKind][32]byte
	observed                            [6]bool
	knownUIDs                           map[uint32]bool
	pending                             *PreparedPublication
	driver                              *BootstrapDriver
	complete, failed, closed, handedOff bool
}

func NewClientSessionBootstrap(binding secure.Binding, generation uint64, w *wallet.Wallet, origin ServerOrigin, key []byte) (*SessionBootstrap, error) {
	if w == nil || len(key) != 32 || generation == 0 || origin.UIDValidity == 0 || origin.User != binding.User {
		return nil, ErrProtocol
	}
	identity := w.Identity()
	if identity.User != binding.User || identity.Session != binding.Session || identity.Store != origin.Store {
		return nil, ErrProtocol
	}
	shape := wire.QuotaKey{Store: origin.Store, Epoch: origin.Run, Grant: 1, Session: binding.Session, Account: origin.Account, User: binding.User}
	if _, err := wire.ServerEnvelopeSize(shape, wire.ServerControl, origin.Folder, 1); err != nil {
		return nil, ErrProtocol
	}
	h, err := secure.NewSessionHandshake(binding, key, true)
	if err != nil {
		return nil, err
	}
	b := &SessionBootstrap{binding: binding, generation: generation, client: true, handshake: h, wallet: w, origin: origin, send: bootstrapRequest, peer: make(map[bootstrapKind][32]byte), own: make(map[bootstrapKind][32]byte), knownUIDs: make(map[uint32]bool)}
	copy(b.key[:], key)
	return b, nil
}

func NewServerSessionBootstrap(binding secure.Binding, generation uint64, store *journal.Store, account, folder string, validity uint32, key []byte) (*SessionBootstrap, error) {
	if store == nil || generation == 0 || len(key) != 32 || validity == 0 {
		return nil, ErrProtocol
	}
	shape := wire.QuotaKey{Store: store.StoreID(), Epoch: store.Run(), Grant: 1, Session: binding.Session, Account: account, User: binding.User}
	if _, err := wire.ServerEnvelopeSize(shape, wire.ServerControl, folder, 1); err != nil {
		return nil, ErrProtocol
	}
	h, err := secure.NewSessionHandshake(binding, key, false)
	if err != nil {
		return nil, err
	}
	b := &SessionBootstrap{binding: binding, generation: generation, store: store, handshake: h, origin: ServerOrigin{Store: store.StoreID(), Run: store.Run(), Account: account, User: binding.User, Folder: folder, UIDValidity: validity}, receive: bootstrapRequest, peer: make(map[bootstrapKind][32]byte), own: make(map[bootstrapKind][32]byte), knownUIDs: make(map[uint32]bool)}
	copy(b.key[:], key)
	return b, nil
}

func (b *SessionBootstrap) bindEpochLocked() error {
	e, err := b.handshake.Epoch()
	if err != nil {
		return err
	}
	c, err := NewCipher(e)
	if err != nil {
		return err
	}
	b.epoch, b.cipher = e, c
	h := sha256.New()
	h.Write([]byte("ITOM/session-bootstrap/transcript/v2\x00"))
	h.Write(b.request)
	h.Write(b.response)
	copy(b.transcript[:], h.Sum(nil))
	return nil
}

func (b *SessionBootstrap) preparePayloadLocked(kind bootstrapKind) ([]byte, error) {
	if kind <= bootstrapResponse {
		return b.handshake.Write(nil)
	}
	if b.epoch == nil {
		return nil, ErrBootstrapPending
	}
	plain := append([]byte{byte(kind)}, b.transcript[:]...)
	defer clear(plain)
	return b.epoch.SealSession(plain, true)
}

func (b *SessionBootstrap) receivePayloadLocked(f bootstrapFrame) error {
	switch f.kind {
	case bootstrapRequest:
		if err := b.handshake.Read(f.payload); err != nil {
			b.failed = true
			return err
		}
		b.request, _ = f.encode()
		b.send, b.receive = bootstrapResponse, 0
	case bootstrapResponse:
		if err := b.handshake.Read(f.payload); err != nil {
			b.failed = true
			return err
		}
		b.response, _ = f.encode()
		if err := b.bindEpochLocked(); err != nil {
			b.failed = true
			return err
		}
		b.send, b.receive = bootstrapClientProof, 0
	default:
		if b.epoch == nil {
			return ErrBootstrapPending
		}
		plains, err := b.epoch.OpenSessionBatch([][]byte{f.payload}, func(plains [][]byte) error {
			if len(plains) != 1 || len(plains[0]) != bootstrapProofBytes || plains[0][0] != byte(f.kind) || !bytes.Equal(plains[0][1:], b.transcript[:]) {
				return ErrProtocol
			}
			return nil
		})
		for _, plain := range plains {
			clear(plain)
		}
		if err != nil {
			return err
		}
		b.receive = 0
		switch f.kind {
		case bootstrapClientProof:
			b.send = bootstrapServerProof
		case bootstrapServerProof:
			b.send = bootstrapClientAck
		case bootstrapClientAck:
			b.send = bootstrapServerAck
		case bootstrapServerAck:
			b.complete = true
		default:
			return ErrProtocol
		}
	}
	return nil
}

// Cipher returns only mutually confirmed keys after this endpoint's last
// physical publication is known accepted. This is not all-carrier READY.
func (b *SessionBootstrap) Cipher() (*Cipher, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.confirmedLocked()
}

func (b *SessionBootstrap) confirmedLocked() (*Cipher, error) {
	if b.closed || b.failed {
		return nil, ErrClosed
	}
	if !b.complete || b.cipher == nil {
		return nil, ErrBootstrapPending
	}
	if b.pending != nil {
		b.pending.mu.Lock()
		finished, outcome := b.pending.finished, b.pending.outcome
		b.pending.mu.Unlock()
		if finished && outcome == Uncertain {
			return nil, ErrUnknown
		}
		if !finished || outcome != Accepted {
			return nil, ErrBootstrapPending
		}
	}
	return b.cipher, nil
}

func (b *SessionBootstrap) Pending() *PreparedPublication {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending
}

// Close stops this bootstrap actor, not its borrowed quota authorities or any
// Cipher already handed to the runtime. Exact charged Pending remains reachable.
func (b *SessionBootstrap) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	clear(b.key[:])
	b.handshake = nil
	return nil
}
