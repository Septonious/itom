package wire

import (
	"crypto/hmac"
	"crypto/sha256"
)

// ServerLedger is authenticated physical origin metadata, not a permission to
// spend or refund. DATA and protocol stores remain distinct authorities.
type ServerLedger uint8

const (
	ServerData ServerLedger = iota + 1
	ServerControl
)

func (class ServerLedger) magic() string {
	switch class {
	case ServerData:
		return "ISD2"
	case ServerControl:
		return "ISC2"
	default:
		return ""
	}
}

func serverEnvelopeMAC(class ServerLedger, key, body []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("ITOM/session-v2/server-envelope/" + class.magic() + "\x00"))
	h.Write(body)
	return h.Sum(nil)
}

func ServerEnvelopeSize(k QuotaKey, class ServerLedger, folder string, encryptedBytes int) (int, error) {
	if class.magic() == "" {
		return 0, ErrFormat
	}
	// Equal fixed framing lengths; neither origin nor class changes the original
	// physical ceilings or canonical MIME accounting geometry.
	return DataEnvelopeSize(k, folder, encryptedBytes)
}

// BindServerEnvelope binds ONE locally charged physical attempt. It is never a
// client UsageReport and cannot register/spend a remote grant. The returned
// descriptor is convenient for trusted local proof, not peer refund authority.
func BindServerEnvelope(k QuotaKey, class ServerLedger, batch ID, folder string, encrypted, key []byte, encodedSize func([]byte) (uint64, error)) (UsageReport, []byte, error) {
	if class.magic() == "" {
		return UsageReport{}, nil, ErrFormat
	}
	mac := func(key, body []byte) []byte { return serverEnvelopeMAC(class, key, body) }
	return bindPhysicalEnvelope(class.magic(), mac, k, 1, batch, 0, folder, encrypted, key, encodedSize)
}

func OpenServerEnvelope(body, key []byte, canonicalBytes uint64) (ServerLedger, UsageReport, []byte, error) {
	if len(key) != 32 || len(body) < 4+sha256.Size || len(body) > MaxDataEnvelopeBody {
		return 0, UsageReport{}, nil, ErrLimit
	}
	var class ServerLedger
	switch string(body[:4]) {
	case ServerData.magic():
		class = ServerData
	case ServerControl.magic():
		class = ServerControl
	default:
		return 0, UsageReport{}, nil, ErrFormat
	}
	n := len(body) - sha256.Size
	if !hmac.Equal(serverEnvelopeMAC(class, key, body[:n]), body[n:]) {
		return 0, UsageReport{}, nil, ErrFormat
	}
	usage, packet, err := parsePhysicalEnvelopeWithDigest(body, sha256.Sum256(body), class.magic())
	if err != nil || usage.Attempt.EncodedBytes != canonicalBytes || usage.Total.Messages != 1 || usage.Total.Bytes != canonicalBytes {
		return 0, UsageReport{}, nil, ErrFormat
	}
	return class, usage, packet, nil
}
