package wire

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode"
	"unicode/utf8"
)

// QuotaKey survives coordinator restarts. Epoch is the issuing journal Run,
// not the Noise epoch. Session identifies the ORIGINAL writer session.
type QuotaKey struct {
	Store         ID
	Epoch, Grant  uint64
	Session       ID
	User, Account string
}
type Quota struct{ Bytes, Messages uint64 }

func (n Quota) valid() bool {
	return n.Bytes <= 1<<40 && n.Messages <= 1<<24 &&
		(n.Bytes == 0) == (n.Messages == 0) && n.Messages <= n.Bytes
}
func (n Quota) within(b Quota) bool { return n.Bytes <= b.Bytes && n.Messages <= b.Messages }

// StorageGrant authorizes only physical storage. It does not authorize LOGIN,
// connections or a writer start without a durable client-side spend.
type StorageGrant struct {
	Key   QuotaKey
	Limit Quota
}

func (StorageGrant) controlKind() Type { return WriteGrant }

// RecoveryFastGrant carries an explicit authenticated service class. Its
// allocation remains an ordinary durable StorageGrant in the wallet/journal.
// Reduced fast leases must not be mistaken for reduced bulk by quota shape.
type RecoveryFastGrant struct{ Offer StorageGrant }

func (RecoveryFastGrant) controlKind() Type { return RecoveryFastWriteGrant }

// GrantRecovery describes an OLD allocation for wallet reconciliation only.
// It never authorizes a Spend or any DATA transmission in the fresh session.
type GrantRecovery struct{ Offer StorageGrant }

func (GrantRecovery) controlKind() Type { return GrantReconcile }

// UsageReport is a cumulative snapshot OR one contiguous physical descriptor.
// A descriptor's Total includes that attempt. Retries use a new ordinal/batch.
type UsageReport struct {
	Key     QuotaKey
	Total   Quota
	Attempt *PhysicalDescriptor
}

func (UsageReport) controlKind() Type { return GrantUsage }

type StorageRelease struct {
	Key   QuotaKey
	Final Quota
}

func (StorageRelease) controlKind() Type { return GrantRelease }

type CleanupStatus struct {
	Key                        QuotaKey
	Used, Registered, Released Quota
	Closed                     bool
}

func (CleanupStatus) controlKind() Type { return GCStatus }

type DescriptorRequest struct {
	Key  QuotaKey
	Next uint64
}

func (DescriptorRequest) controlKind() Type { return GCProofRequest }

type PhysicalDescriptor struct {
	Batch                      ID
	EncodedBytes, DecodedBytes uint64
	Digest                     [32]byte
	Folder                     string
}

func validQuotaAccount(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func validQuotaFolder(s string) bool {
	if len(s) == 0 || len(s) > 255 || !utf8.ValidString(s) || strings.EqualFold(s, "INBOX") {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (k QuotaKey) valid() bool {
	if k.Store == (ID{}) || k.Session == (ID{}) || k.Epoch == 0 || k.Grant == 0 ||
		len(k.User) != 8 || k.User[0] == '0' || !validQuotaAccount(k.Account) {
		return false
	}
	for _, c := range []byte(k.User) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func encodeQuotaKey(k QuotaKey) ([]byte, error) {
	if !k.valid() {
		return nil, ErrFormat
	}
	p := append([]byte(nil), k.Store[:]...)
	p = append(p, words(k.Epoch, k.Grant)...)
	p = append(p, k.Session[:]...)
	p = append(p, []byte(k.User)...)
	p = append(p, byte(len(k.Account)))
	return append(p, []byte(k.Account)...), nil
}
func decodeQuotaKey(p []byte) (QuotaKey, []byte, error) {
	var k QuotaKey
	if len(p) < 58 || p[56] == 0 || p[56] > 64 || len(p) < 57+int(p[56]) {
		return k, nil, ErrFormat
	}
	n := 57 + int(p[56])
	copy(k.Store[:], p[:16])
	k.Epoch = binary.BigEndian.Uint64(p[16:])
	k.Grant = binary.BigEndian.Uint64(p[24:])
	copy(k.Session[:], p[32:48])
	k.User = string(p[48:56])
	k.Account = string(p[57:n])
	if !k.valid() {
		return QuotaKey{}, nil, ErrFormat
	}
	return k, p[n:], nil
}
func quotaBytes(n Quota) []byte { return words(n.Bytes, n.Messages) }
func parseQuota(p []byte) Quota {
	return Quota{Bytes: binary.BigEndian.Uint64(p), Messages: binary.BigEndian.Uint64(p[8:])}
}
func (d PhysicalDescriptor) valid() bool {
	return d.Batch != (ID{}) && d.Digest != ([32]byte{}) && d.DecodedBytes > 0 &&
		d.DecodedBytes <= 1<<20 && d.EncodedBytes >= d.DecodedBytes && d.EncodedBytes <= 2<<20 && validQuotaFolder(d.Folder)
}

// Retirement confirms receipt of final GC status; Ack only confirms removal of
// metadata. Neither direction authorizes spending or proves physical cleanup.
type Retirement struct {
	Key   QuotaKey
	Final Quota
	Ack   bool
}

func (r Retirement) controlKind() Type {
	if r.Ack {
		return GrantRetireAck
	}
	return GrantRetire
}

func encodeGrantControl(m Control) (Frame, error) {
	if fast, ok := m.(RecoveryFastGrant); ok {
		f, err := encodeGrantControl(fast.Offer)
		f.Type = RecoveryFastWriteGrant
		return f, err
	}
	if recovery, ok := m.(GrantRecovery); ok {
		f, err := encodeGrantControl(recovery.Offer)
		f.Type = GrantReconcile
		return f, err
	}
	var k QuotaKey
	var tail []byte
	switch x := m.(type) {
	case StorageGrant:
		if !x.Limit.valid() || x.Limit.Messages == 0 {
			return Frame{}, ErrLimit
		}
		k = x.Key
		tail = quotaBytes(x.Limit)
	case UsageReport:
		if !x.Total.valid() {
			return Frame{}, ErrLimit
		}
		k = x.Key
		tail = append(quotaBytes(x.Total), 0)
		if x.Attempt != nil {
			d := *x.Attempt
			if !d.valid() || x.Total.Messages == 0 || d.EncodedBytes > x.Total.Bytes {
				return Frame{}, ErrFormat
			}
			tail[16] = 1
			tail = append(tail, d.Batch[:]...)
			tail = append(tail, words(d.EncodedBytes, d.DecodedBytes)...)
			tail = append(tail, d.Digest[:]...)
			tail = append(tail, byte(len(d.Folder)))
			tail = append(tail, []byte(d.Folder)...)
		}
	case Retirement:
		if !x.Final.valid() {
			return Frame{}, ErrLimit
		}
		k = x.Key
		tail = quotaBytes(x.Final)
	case StorageRelease:
		if !x.Final.valid() {
			return Frame{}, ErrLimit
		}
		k = x.Key
		tail = quotaBytes(x.Final)
	case CleanupStatus:
		if !x.Used.valid() || !x.Registered.valid() || !x.Released.valid() || !x.Released.within(x.Registered) || !x.Registered.within(x.Used) {
			return Frame{}, ErrFormat
		}
		k = x.Key
		tail = append(quotaBytes(x.Used), quotaBytes(x.Registered)...)
		tail = append(tail, quotaBytes(x.Released)...)
		flag := byte(0)
		if x.Closed {
			flag = 1
		}
		tail = append(tail, flag)
	case DescriptorRequest:
		if x.Next == 0 || x.Next > 1<<24 {
			return Frame{}, ErrLimit
		}
		k = x.Key
		tail = words(x.Next)
	default:
		return Frame{}, ErrUnsupportedControl
	}
	p, err := encodeQuotaKey(k)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: m.controlKind(), Payload: append(p, tail...)}, nil
}
func decodeGrantControl(f Frame) (Control, error) {
	k, p, err := decodeQuotaKey(f.Payload)
	if err != nil {
		return nil, err
	}
	switch f.Type {
	case WriteGrant, GrantReconcile, RecoveryFastWriteGrant:
		if len(p) != 16 {
			return nil, ErrFormat
		}
		offer := StorageGrant{Key: k, Limit: parseQuota(p)}
		if f.Type == GrantReconcile {
			return GrantRecovery{Offer: offer}, nil
		}
		if f.Type == RecoveryFastWriteGrant {
			return RecoveryFastGrant{Offer: offer}, nil
		}
		return offer, nil
	case GrantRetire, GrantRetireAck:
		if len(p) != 16 {
			return nil, ErrFormat
		}
		return Retirement{Key: k, Final: parseQuota(p), Ack: f.Type == GrantRetireAck}, nil
	case GrantRelease:
		if len(p) != 16 {
			return nil, ErrFormat
		}
		return StorageRelease{Key: k, Final: parseQuota(p)}, nil
	case GCStatus:
		if len(p) != 49 || p[48] > 1 {
			return nil, ErrFormat
		}
		return CleanupStatus{Key: k, Used: parseQuota(p), Registered: parseQuota(p[16:]), Released: parseQuota(p[32:]), Closed: p[48] == 1}, nil
	case GCProofRequest:
		if len(p) != 8 {
			return nil, ErrFormat
		}
		return DescriptorRequest{Key: k, Next: binary.BigEndian.Uint64(p)}, nil
	case GrantUsage:
		if len(p) < 17 || p[16] > 1 {
			return nil, ErrFormat
		}
		x := UsageReport{Key: k, Total: parseQuota(p)}
		if p[16] == 0 {
			if len(p) != 17 {
				return nil, ErrFormat
			}
			return x, nil
		}
		if len(p) < 83 || int(p[81])+82 != len(p) {
			return nil, ErrFormat
		}
		d := &PhysicalDescriptor{EncodedBytes: binary.BigEndian.Uint64(p[33:]), DecodedBytes: binary.BigEndian.Uint64(p[41:]), Folder: string(p[82:])}
		copy(d.Batch[:], p[17:33])
		copy(d.Digest[:], p[49:81])
		x.Attempt = d
		return x, nil
	}
	return nil, ErrUnsupportedControl
}

// BindPhysical binds an opaque encrypted batch to exactly ONE physical attempt.
// This clear wrapper is NOT encryption/authentication. Its entire digest must
// arrive through authenticated GRANT_USAGE and its body through secure.Open.
// It prevents one mailbox message from proving cleanup of two grant ordinals.
func BindPhysical(k QuotaKey, ordinal uint64, batch ID, encrypted []byte) ([]byte, error) {
	if ordinal == 0 || ordinal > 1<<24 || batch == (ID{}) || len(encrypted) == 0 || len(encrypted) > 1<<20 {
		return nil, ErrLimit
	}
	p, err := encodeQuotaKey(k)
	if err != nil {
		return nil, err
	}
	out := append([]byte("ITQ1"), p...)
	out = append(out, words(ordinal)...)
	out = append(out, batch[:]...)
	out = append(out, encrypted...)
	if len(out) > 1<<20 {
		return nil, ErrLimit
	}
	return out, nil
}

// CheckPhysical checks ownership only; it must not be used to deliver plaintext.
func CheckPhysical(raw []byte, k QuotaKey, ordinal uint64, batch ID) bool {
	if bytes.HasPrefix(raw, []byte(DataEnvelopeMagic)) {
		r, _, err := parseDataEnvelope(raw)
		return err == nil && r.Key == k && ordinal != 0 && r.Total.Messages == ordinal && r.Attempt.Batch == batch
	}
	if len(raw) > 1<<20 || len(raw) < 4 || !bytes.Equal(raw[:4], []byte("ITQ1")) {
		return false
	}
	got, p, err := decodeQuotaKey(raw[4:])
	if err != nil || got != k || len(p) <= 24 {
		return false
	}
	return ordinal != 0 && binary.BigEndian.Uint64(p) == ordinal && bytes.Equal(p[8:24], batch[:])
}
