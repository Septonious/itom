package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
)

// ErrUnsupportedControl rejects controls with no executable contract (rekey).
var ErrUnsupportedControl = errors.New("CONTROL_UNSUPPORTED")

const MaxNackRanges = 32

type Reason uint16

const (
	ReasonNone Reason = iota
	ReasonRefused
	ReasonTimeout
	ReasonPolicy
	ReasonCapacity
	ReasonProtocol
	ReasonClosed
	ReasonNetwork
)

func (r Reason) valid() bool { return r <= ReasonNetwork }

// Control is sealed to this package. EncodeControl accepts the value types
// below, not pointers. DecodeControl must run AFTER record authentication.
// Structural decoding grants neither application credit nor physical quota.
type Control interface{ controlKind() Type }

type Activation struct{ Kind Type }

func (m Activation) controlKind() Type { return m.Kind }

// Coordinator is carried only inside an authenticated Noise record.
// Features=1 requires the explicit remote-grant retirement handshake.
type Coordinator struct {
	Store    ID
	Run      uint64
	Features uint64
	Ack      bool
}

func (m Coordinator) controlKind() Type {
	if m.Ack {
		return CoordinatorAck
	}
	return CoordinatorInfo
}

// MailReceived acknowledges retained authenticated records, not physical GC.
// Counters refer to the opposite Noise direction in this exact session.
// Receipts never acknowledge receipts, avoiding an idle acknowledgement loop.
type MailReceived struct{ Counters []uint64 }

func (m MailReceived) controlKind() Type { return MailReceipt }

type Connect struct {
	Host string
	Port uint16
}

func (m Connect) controlKind() Type { return Open }

type ConnectResult struct{ Code Reason }

func (m ConnectResult) controlKind() Type {
	if m.Code == ReasonNone {
		return OpenOK
	}
	return OpenError
}

type Received struct{ Frontier uint64 }

func (m Received) controlKind() Type { return Receipt }

type Credit struct{ MaxStream, MaxData uint64 }

func (m Credit) controlKind() Type { return Window }

// Progress describes the sender's attempted DATA end and optional final end.
// Receipt and both credit limits describe its receiver (the opposite byte flow).
type Progress struct {
	Attempted, Receipt, MaxStream, MaxData uint64
	HasFinal                               bool
	Final                                  uint64
}

func (m Progress) controlKind() Type { return Status }

type Probe struct{}

func (m Probe) controlKind() Type { return StatusRequest }

type ByteRange struct{ Start, End uint64 }
type Missing struct {
	ID     uint64
	Ranges []ByteRange
}

func (m Missing) controlKind() Type { return Nack }

type Finish struct {
	Ack   bool
	Final uint64
}

func (m Finish) controlKind() Type {
	if m.Ack {
		return FinAck
	}
	return Fin
}

type Abort struct {
	Ack   bool
	Final uint64
	Code  Reason
}

func (m Abort) controlKind() Type {
	if m.Ack {
		return ResetAck
	}
	return Reset
}

type Echo struct {
	Reply bool
	Token uint64
}

func (m Echo) controlKind() Type {
	if m.Reply {
		return Pong
	}
	return Ping
}

type EndSession struct {
	Failure bool
	Code    Reason
}

func (m EndSession) controlKind() Type {
	if m.Failure {
		return SessionError
	}
	return SessionClose
}

// Hosts are canonical ASCII DNS names (IDNA A-labels) or canonical IP literals.
// Parsing never resolves DNS. IPv6 zones, URIs, wildcards and whitespace fail.
func ValidHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && !ip.Is4In6() && ip.String() == host
	}
	if host != strings.ToLower(host) {
		return false
	}
	// A failed IP parse must not fall back to an ambiguous all-numeric DNS
	// form such as 127.0.0.01 (some downstream resolvers interpret it as IP).
	numeric := true
	for _, c := range []byte(host) {
		if !(c >= '0' && c <= '9' || c == '.') {
			numeric = false
			break
		}
	}
	if numeric {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func words(values ...uint64) []byte {
	b := make([]byte, 8*len(values))
	for i, n := range values {
		binary.BigEndian.PutUint64(b[8*i:], n)
	}
	return b
}
func reasonBytes(code Reason) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(code))
	return b
}

func EncodeControl(stream uint64, message Control) (Frame, error) {
	if message == nil {
		return Frame{}, ErrFormat
	}
	// Type assertion before calling methods also rejects typed nil pointers.
	f := Frame{Stream: stream}
	switch m := message.(type) {
	case Activation:
		if m.Kind != Confirm && m.Kind != Ready && m.Kind != ReadyAck {
			return Frame{}, ErrFormat
		}
		f.Type = m.Kind
	case Notice:
		if m.Revision == 0 || m.Revision > MaxOffset || !ValidNoticeText(m.Text) {
			return Frame{}, ErrFormat
		}
		f.Type, f.Payload = ServerMessage, append(words(m.Revision), []byte(m.Text)...)
	case NoticeAck:
		if m.Revision == 0 || m.Revision > MaxOffset {
			return Frame{}, ErrFormat
		}
		f.Type, f.Payload = ServerMessageAck, words(m.Revision)
	case Coordinator:
		if m.Store == (ID{}) || m.Run == 0 || m.Features != 1 {
			return Frame{}, ErrFormat
		}
		f.Type = m.controlKind()
		f.Payload = append(append([]byte(nil), m.Store[:]...), words(m.Run, m.Features)...)
	case MailReceived:
		if len(m.Counters) == 0 || len(m.Counters) > 512 {
			return Frame{}, ErrLimit
		}
		f.Type = MailReceipt
		var previous uint64
		for i, n := range m.Counters {
			if n > MaxOffset || (i > 0 && n <= previous) {
				return Frame{}, ErrFormat
			}
			f.Payload = append(f.Payload, words(n)...)
			previous = n
		}
	case Connect:
		if !ValidHost(m.Host) || m.Port == 0 {
			return Frame{}, ErrFormat
		}
		f.Type = Open
		f.Payload = make([]byte, 4+len(m.Host))
		binary.BigEndian.PutUint16(f.Payload, m.Port)
		binary.BigEndian.PutUint16(f.Payload[2:], uint16(len(m.Host)))
		copy(f.Payload[4:], m.Host)
	case ConnectResult:
		if !m.Code.valid() {
			return Frame{}, ErrFormat
		}
		f.Type = m.controlKind()
		if m.Code != ReasonNone {
			f.Payload = reasonBytes(m.Code)
		}
	case Received:
		f.Type, f.Offset = Receipt, m.Frontier
	case Credit:
		if m.MaxData > MaxOffset {
			return Frame{}, ErrLimit
		}
		f.Type, f.Offset, f.Payload = Window, m.MaxStream, words(m.MaxData)
	case Progress:
		if m.Receipt > m.MaxStream || m.MaxStream > MaxOffset || m.MaxData > MaxOffset ||
			m.Final > MaxOffset || (m.HasFinal && m.Final < m.Attempted) || (!m.HasFinal && m.Final != 0) {
			return Frame{}, ErrFormat
		}
		f.Type, f.Offset = Status, m.Attempted
		f.Payload = words(m.Receipt, m.MaxStream, m.MaxData, m.Final)
		flag := byte(0)
		if m.HasFinal {
			flag = 1
		}
		f.Payload = append(f.Payload, flag)
	case Probe:
		f.Type = StatusRequest
	case Missing:
		if m.ID == 0 || len(m.Ranges) == 0 || len(m.Ranges) > MaxNackRanges {
			return Frame{}, ErrLimit
		}
		f.Type = Nack
		f.Payload = words(m.ID)
		f.Payload = append(f.Payload, byte(len(m.Ranges)))
		var end uint64
		for i, r := range m.Ranges {
			if r.Start >= r.End || r.End > MaxOffset || (i > 0 && r.Start <= end) {
				return Frame{}, ErrFormat
			}
			f.Payload = append(f.Payload, words(r.Start, r.End)...)
			end = r.End
		}
	case Finish:
		f.Type, f.Offset = m.controlKind(), m.Final
	case Abort:
		if !m.Code.valid() || m.Code == ReasonNone {
			return Frame{}, ErrFormat
		}
		f.Type, f.Offset, f.Payload = m.controlKind(), m.Final, reasonBytes(m.Code)
	case Echo:
		if m.Token == 0 {
			return Frame{}, ErrFormat
		}
		f.Type, f.Payload = m.controlKind(), words(m.Token)
	case StorageGrant, GrantRecovery, RecoveryFastGrant, UsageReport, StorageRelease, CleanupStatus, DescriptorRequest, Retirement:
		var err error
		f, err = encodeGrantControl(message)
		if err != nil {
			return Frame{}, err
		}
		f.Stream = stream
	case EndSession:
		if !m.Code.valid() || (m.Failure && m.Code == ReasonNone) {
			return Frame{}, ErrFormat
		}
		f.Type, f.Payload = m.controlKind(), reasonBytes(m.Code)
	default:
		return Frame{}, ErrUnsupportedControl
	}
	if err := f.Validate(); err != nil {
		return Frame{}, err
	}
	return f, nil
}

func DecodeControl(f Frame) (Control, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	p := f.Payload
	var m Control
	switch f.Type {
	case ServerMessage:
		if len(p) < 8 || len(p) > 8+MaxNoticeText {
			return nil, ErrFormat
		}
		m = Notice{Revision: binary.BigEndian.Uint64(p[:8]), Text: string(p[8:])}
	case ServerMessageAck:
		if len(p) != 8 {
			return nil, ErrFormat
		}
		m = NoticeAck{Revision: binary.BigEndian.Uint64(p)}
	case WriteGrant, RecoveryFastWriteGrant, GrantReconcile, GrantUsage, GrantRelease, GCStatus, GCProofRequest, GrantRetire, GrantRetireAck:
		var err error
		m, err = decodeGrantControl(f)
		if err != nil {
			return nil, err
		}
	case MailReceipt:
		if len(p) == 0 || len(p)%8 != 0 || len(p) > 512*8 {
			return nil, ErrFormat
		}
		counters := make([]uint64, len(p)/8)
		for i := range counters {
			counters[i] = binary.BigEndian.Uint64(p[i*8:])
		}
		m = MailReceived{Counters: counters}
	case CoordinatorInfo, CoordinatorAck:
		if len(p) != 32 {
			return nil, ErrFormat
		}
		x := Coordinator{Run: binary.BigEndian.Uint64(p[16:]), Features: binary.BigEndian.Uint64(p[24:]), Ack: f.Type == CoordinatorAck}
		copy(x.Store[:], p[:16])
		m = x
	case Confirm, Ready, ReadyAck:
		if len(p) != 0 {
			return nil, ErrFormat
		}
		m = Activation{Kind: f.Type}
	case Open:
		if len(p) < 4 || len(p) > 257 || int(binary.BigEndian.Uint16(p[2:])) != len(p)-4 {
			return nil, ErrFormat
		}
		m = Connect{Host: string(p[4:]), Port: binary.BigEndian.Uint16(p)}
	case OpenOK:
		if len(p) != 0 {
			return nil, ErrFormat
		}
		m = ConnectResult{}
	case OpenError:
		if len(p) != 2 || binary.BigEndian.Uint16(p) == 0 {
			return nil, ErrFormat
		}
		m = ConnectResult{Code: Reason(binary.BigEndian.Uint16(p))}
	case Receipt:
		if len(p) != 0 {
			return nil, ErrFormat
		}
		m = Received{Frontier: f.Offset}
	case Window:
		if len(p) != 8 {
			return nil, ErrFormat
		}
		m = Credit{MaxStream: f.Offset, MaxData: binary.BigEndian.Uint64(p)}
	case Status:
		if len(p) != 33 || p[32] > 1 {
			return nil, ErrFormat
		}
		m = Progress{Attempted: f.Offset, Receipt: binary.BigEndian.Uint64(p),
			MaxStream: binary.BigEndian.Uint64(p[8:]), MaxData: binary.BigEndian.Uint64(p[16:]),
			Final: binary.BigEndian.Uint64(p[24:]), HasFinal: p[32] == 1}
	case StatusRequest:
		if len(p) != 0 {
			return nil, ErrFormat
		}
		m = Probe{}
	case Nack:
		if len(p) < 9 || p[8] == 0 || p[8] > MaxNackRanges || len(p) != 9+16*int(p[8]) {
			return nil, ErrFormat
		}
		x := Missing{ID: binary.BigEndian.Uint64(p), Ranges: make([]ByteRange, int(p[8]))}
		for i := range x.Ranges {
			x.Ranges[i] = ByteRange{Start: binary.BigEndian.Uint64(p[9+16*i:]), End: binary.BigEndian.Uint64(p[17+16*i:])}
		}
		m = x
	case Fin, FinAck:
		if len(p) != 0 {
			return nil, ErrFormat
		}
		m = Finish{Ack: f.Type == FinAck, Final: f.Offset}
	case Reset, ResetAck:
		if len(p) != 2 {
			return nil, ErrFormat
		}
		m = Abort{Ack: f.Type == ResetAck, Final: f.Offset, Code: Reason(binary.BigEndian.Uint16(p))}
	case Ping, Pong:
		if len(p) != 8 {
			return nil, ErrFormat
		}
		m = Echo{Reply: f.Type == Pong, Token: binary.BigEndian.Uint64(p)}
	case SessionClose, SessionError:
		if len(p) != 2 {
			return nil, ErrFormat
		}
		m = EndSession{Failure: f.Type == SessionError, Code: Reason(binary.BigEndian.Uint16(p))}
	default:
		return nil, ErrUnsupportedControl
	}
	// A single canonical encoder owns semantic validation. This also rejects
	// ignored offset fields and alternative encodings, rather than normalizing.
	canonical, err := EncodeControl(f.Stream, m)
	if err != nil {
		return nil, err
	}
	if canonical.Type != f.Type || canonical.Offset != f.Offset || !bytes.Equal(canonical.Payload, p) {
		return nil, ErrFormat
	}
	return m, nil
}
