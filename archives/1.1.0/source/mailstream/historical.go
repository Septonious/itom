package mailstream

import (
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	imap "github.com/emersion/go-imap/v2"
)

// A freshly authenticated replacement abandons the previous session. Recover
// only this identity's old server-produced v3 DATA. Never erase debt merely
// because a new session began. Control, client DATA and legacy mail stay out.
func (l *IMAPLink) oldOutbound(name string, current SessionID) bool {
	prefix := "itom3-" + l.identity + "-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "-")
	if len(parts) != 3 || len(parts[0]) != 32 || parts[1] != "s2c" || len(parts[2]) != 8 {
		return false
	}
	raw, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var id SessionID
	copy(id[:], raw)
	_, err = strconv.ParseUint(parts[2], 16, 32)
	return err == nil && id != (SessionID{}) && id != current
}

// Historical metadata work runs once, before this account advertises serving
// roles. Providers can serialize namespace operations across all sockets of an
// account; a separate maintenance socket alone does not isolate live traffic.
func (a *mailAccountState) recoverHistorical(p *mailPipe) error {
	if a.link.client || a.recovered == 0 {
		return nil
	}
	a.phase("historical_recovery")
	if err := p.conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	defer p.conn.SetDeadline(time.Time{})
	rows, err := p.client.List("", "itom3-"+a.link.identity+"-*", nil).Collect()
	if err != nil {
		return err
	}
	if len(rows) > 4096 {
		return ErrCapacity
	}
	for _, row := range rows {
		a.link.mu.Lock()
		current := a.link.attached
		a.link.mu.Unlock()
		folder := row.Mailbox
		if !a.link.oldOutbound(folder, current) {
			continue
		}
		selected, err := p.client.Select(folder, nil).Wait()
		if err != nil {
			return err
		}
		if selected.NumMessages > segmentAppends {
			return ErrProtocol
		}
		bytes := int64(0)
		if selected.NumMessages > 0 {
			messages, err := p.client.Fetch(imap.SeqSet{{Start: 1, Stop: selected.NumMessages}}, &imap.FetchOptions{RFC822Size: true}).Collect()
			if err != nil {
				return err
			}
			if len(messages) != int(selected.NumMessages) {
				return ErrProtocol
			}
			for _, message := range messages {
				if message.RFC822Size <= 0 || message.RFC822Size > 2*maxMailPlain {
					return ErrProtocol
				}
				bytes += message.RFC822Size
			}
		}
		if p.client.Caps().Has(imap.CapUnselect) {
			err = p.client.Unselect().Wait()
		} else {
			// Never select a live control folder on a second connection, and
			// never CLOSE/EXPUNGE a mailbox. This empty parking folder is ours.
			parking := "itom3-" + a.link.identity + "-maintenance"
			if e := p.client.Create(parking, nil).Wait(); e != nil {
				var tagged *imap.Error
				if !errors.As(e, &tagged) {
					return e
				}
			}
			_, err = p.client.Select(parking, nil).Wait()
		}
		if err != nil {
			return err
		}
		a.link.mu.Lock()
		current = a.link.attached
		a.link.mu.Unlock()
		if !a.link.oldOutbound(folder, current) {
			continue
		}
		if err = p.client.Delete(folder).Wait(); err != nil {
			return err
		}
		remaining, err := p.client.List("", folder, nil).Collect()
		if err != nil {
			return err
		}
		if len(remaining) != 0 {
			return ErrProtocol
		}
		a.mu.Lock()
		if bytes > int64(a.spent) {
			a.mu.Unlock()
			return ErrStorage
		}
		err = a.refundLocked(int(bytes))
		a.mu.Unlock()
		if err != nil {
			return err
		}
		a.retired.Add(1)
	}
	return nil
}
