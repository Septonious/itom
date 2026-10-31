package transport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

// A receipt is emitted only for a complete authenticated SERVER body after
// every record has applied/discarded. It consumes bounded existing metadata,
// never another message or grant. It is NOT deletion/absence proof, a logical
// DATA receipt, or a measurement of RTT/useful bandwidth.
type deliveryReceipt struct {
	uid         uint32
	keyword     string
	command     *Command
	format      int
	initialized bool
}

type DeliveryReceiptStats struct {
	Queued, Marked, Refused uint64
	Pending                 int
	Disabled                bool
}

func receiptKeyword(body []byte, validity, uid uint32, key []byte) string {
	digest := sha256.Sum256(body)
	m := hmac.New(sha256.New, key)
	m.Write([]byte("ITOM/session-v2/received-body/v1\x00"))
	m.Write(digest[:]) // includes original store/run/session/account/folder/batch
	var location [8]byte
	binary.BigEndian.PutUint32(location[:4], validity)
	binary.BigEndian.PutUint32(location[4:], uid)
	m.Write(location[:])
	return "ITOM2R_" + hex.EncodeToString(m.Sum(nil))
}

// EnableDeliveryReceipts opts a client inbox into peer delivery metadata.
// The issuer still needs its original observed BODY and same-incarnation
// post-expunge absence proof before it can release physical journal debt.
func (i *Inbox) EnableDeliveryReceipts() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.server || i.session == nil || i.admit == nil {
		return ErrProtocol
	}
	i.session.mu.Lock()
	defer i.session.mu.Unlock()
	if i.session.services != 0 || i.session.runtime != nil {
		return ErrProtocol
	}
	if !i.executor.keywords {
		return ErrKeywordsUnsupported
	}
	if i.receipts == nil {
		i.receipts = make(map[uint32]*deliveryReceipt)
	}
	i.ensureMaintenanceWake()
	i.receiptEnabled = true
	for uid, keyword := range i.startupCleanup.receipts {
		i.receipts[uid] = &deliveryReceipt{uid: uid, keyword: keyword}
		i.receiptStats.Queued++
	}
	i.startupCleanup.receipts = nil
	if len(i.receipts) > 0 {
		i.signalMaintenance()
	}
	return nil
}

func (i *Inbox) DeliveryReceiptStats() DeliveryReceiptStats {
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.receiptStats
	s.Pending = len(i.receipts)
	return s
}

func (i *Inbox) markReceiptOne(ctx context.Context) (bool, error) {
	i.maintenanceMu.Lock()
	defer i.maintenanceMu.Unlock()
	i.mu.Lock()
	if !i.receiptEnabled || len(i.receipts) == 0 {
		i.mu.Unlock()
		return false, nil
	}
	var uid uint32
	for next := range i.receipts {
		if uid == 0 || next < uid {
			uid = next
		}
	}
	r := i.receipts[uid]
	i.mu.Unlock()
	if !r.initialized {
		r.initialized = true
		preferred := i.executor.preferredKeywordBytes()
		for r.format < 3 && preferred > 0 && []int{71, 59, 31, 16}[r.format] > preferred {
			r.format++
		}
	}
	if r.command == nil {
		var err error
		flags := []string{r.keyword}
		if r.format > 0 {
			flags = keywordFragments(r.keyword[7:], 'r', []int{0, 48, 20, 5}[r.format])
		}
		r.command, err = i.executor.storeKeywords(ctx, uid, flags, true)
		if err != nil {
			return false, err
		}
	}
	if _, err := r.command.Wait(ctx); err != nil {
		if errors.Is(err, ErrRefused) {
			i.mu.Lock()
			i.receiptStats.Refused++
			if r.format == 3 {
				i.receiptEnabled = false
				i.receiptStats.Disabled = true
			}
			i.mu.Unlock()
			if r.format < 3 {
				r.format++
				r.command = nil
				return true, nil // bounded metadata fallback, not a marked receipt
			}
			return false, nil
		}
		return false, err
	}
	i.executor.rememberKeywordBytes([]int{71, 59, 31, 16}[r.format])
	i.mu.Lock()
	delete(i.receipts, uid)
	i.receiptStats.Marked++
	i.mu.Unlock()
	return true, nil
}

func verifiedReceiptKeyword(flags []string, expected string) bool {
	if len(expected) != 71 || !strings.EqualFold(expected[:7], "ITOM2R_") {
		return false
	}
	for _, flag := range flags {
		if hmac.Equal([]byte(strings.ToLower(flag)), []byte(strings.ToLower(expected))) {
			return true
		}
	}
	want := strings.ToLower(expected[7:])
	for payload := range decodeKeywordFragments(flags, 'r', 64) {
		if hmac.Equal([]byte(payload), []byte(want)) {
			return true
		}
	}
	return false
}

// The short maintenance-state lock is held when queuing. Wakes are bounded hints, not authority.
func (i *Inbox) ensureMaintenanceWake() {
	if i.maintenanceChanged == nil {
		i.maintenanceChanged = make(chan struct{}, 1)
	}
}
func (i *Inbox) signalMaintenance() {
	select {
	case i.maintenanceChanged <- struct{}{}:
	default:
	}
}
