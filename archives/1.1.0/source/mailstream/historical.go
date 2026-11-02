package mailstream

import (
	"encoding/hex"
	"strconv"
	"strings"

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

// Caller holds Account.mu. The existing independent maintenance connection
// and bounded timeout isolate collection from delivery. Failure retains debt.
func (a *mailAccountState) collectHistoricalLocked() {
	if a.link.client || a.id == (SessionID{}) || a.historicalQueued || a.historicalRefused {
		return
	}
	id := a.id
	if a.historicalFor == id && len(a.historicalFolders) == 0 {
		return
	}
	var folder string
	if a.historicalFor == id {
		folder = a.historicalFolders[0]
	}
	err := a.config.Maintenance.submit(maintenanceJob{ctx: a.ctx, run: func(p *mailPipe) error {
		if !a.attachMu.TryLock() {
			a.mu.Lock()
			a.historicalQueued = false
			a.mu.Unlock()
			return nil
		}
		defer a.attachMu.Unlock()
		a.mu.Lock()
		current := a.id
		a.mu.Unlock()
		a.link.mu.Lock()
		attached := a.link.attached
		a.link.mu.Unlock()
		if current != id || attached != id {
			a.mu.Lock()
			a.historicalQueued = false
			a.mu.Unlock()
			return nil
		}
		if folder == "" {
			rows, err := p.client.List("", "itom3-"+a.link.identity+"-*", nil).Collect()
			if err != nil {
				return err
			}
			var folders []string
			for _, row := range rows {
				if a.link.oldOutbound(row.Mailbox, id) {
					a.mu.Lock()
					known := false
					for _, owned := range a.segments {
						if owned.folder == row.Mailbox {
							known = true
							break
						}
					}
					a.mu.Unlock()
					if known {
						continue
					}
					if len(folders) >= 4096 {
						return ErrCapacity
					}
					folders = append(folders, row.Mailbox)
				}
			}
			a.mu.Lock()
			a.historicalFor, a.historicalFolders, a.historicalQueued = id, folders, false
			a.mu.Unlock()
			return nil
		}
		if !a.link.oldOutbound(folder, id) {
			return ErrProtocol
		}
		selected, err := p.client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return err
		}
		if selected.NumMessages > segmentAppends {
			return ErrProtocol
		}
		bytes := int64(0)
		if selected.NumMessages > 0 {
			rows, err := p.client.Fetch(imap.SeqSet{{Start: 1, Stop: selected.NumMessages}}, &imap.FetchOptions{RFC822Size: true}).Collect()
			if err != nil {
				return err
			}
			if len(rows) != int(selected.NumMessages) {
				return ErrProtocol
			}
			for _, row := range rows {
				if row.RFC822Size <= 0 || row.RFC822Size > 2*maxMailPlain {
					return ErrProtocol
				}
				bytes += row.RFC822Size
			}
		}
		// Never CLOSE/EXPUNGE. Without UNSELECT select an existing control
		// folder read-only on this maintenance connection, then DELETE.
		if p.client.Caps().Has(imap.CapUnselect) {
			err = p.client.Unselect().Wait()
		} else {
			_, err = p.client.Select(a.link.controlFolder(false), &imap.SelectOptions{ReadOnly: true}).Wait()
		}
		if err != nil {
			return err
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
		defer a.mu.Unlock()
		// Refund actual deleted RFC822 bytes only. Unknown APPEND outcomes
		// and unused crash reservations remain charged conservatively.
		if bytes > int64(a.spent) {
			return ErrStorage
		}
		if err := a.refundLocked(int(bytes)); err != nil {
			return err
		}
		a.retired.Add(1)
		a.historicalFolders = a.historicalFolders[1:]
		a.historicalQueued = false
		return nil
	}, failed: func(error) {
		a.mu.Lock()
		a.historicalRefused = true
		a.historicalQueued = false
		a.mu.Unlock()
	}})
	if err == nil {
		a.historicalQueued = true
	}
}
