package mailstream

import (
	"context"
	"sort"
	"sync"
	"time"
)

// One mutation lease is shared by all server identities. Unknown results block
// further collection on the same physical account, not independent accounts.
// A timeout never grants storage credit. Only the later physical audit does.
type ReclamationCoordinator struct {
	mu          sync.Mutex
	state       ReclamationSnapshot
	active      bool
	waiting     []*mailAccountState
	quarantines map[string]*reclamationFailure
	sessions    map[*IMAPLink]bool
	quietSince  time.Time
	idleStop    context.CancelFunc
}
type reclamationFailure struct {
	owner                 *mailAccountState
	folder, stage, reason string
	retryAt               time.Time
	failures              uint
}
type ReclamationSnapshot struct {
	Paused      bool                    `json:"paused"`
	Stage       string                  `json:"stage,omitempty"`
	Failure     string                  `json:"failure,omitempty"`
	Completed   uint64                  `json:"completed"`
	Folder      string                  `json:"folder,omitempty"`
	RetryAtMS   int64                   `json:"retry_at_ms,omitempty"`
	Quarantines []ReclamationQuarantine `json:"quarantined_accounts,omitempty"`
}
type ReclamationQuarantine struct {
	Account   string `json:"account"`
	Folder    string `json:"folder"`
	Stage     string `json:"stage"`
	Failure   string `json:"failure"`
	RetryAtMS int64  `json:"retry_at_ms"`
}

func NewReclamationCoordinator() *ReclamationCoordinator {
	return &ReclamationCoordinator{quietSince: time.Now()}
}
func reclamationAccountKey(a *mailAccountState) string {
	if len(a.storageKey) > 0 {
		return string(a.storageKey)
	}
	return string(retentionKey(a.config))
}

// Routing aliases are not physical-account identities. A stopped owner can be
// replaced only by the same namespace on the same physical mailbox.
func (c *ReclamationCoordinator) failureLocked(a *mailAccountState) *reclamationFailure {
	f := c.quarantines[reclamationAccountKey(a)]
	if f != nil && f.owner != a && f.owner.ctx.Err() != nil && f.owner.link.identity == a.link.identity {
		f.owner = a
	}
	return f
}
func (c *ReclamationCoordinator) permittedLocked(a *mailAccountState, folder string) bool {
	f := c.failureLocked(a)
	return f == nil || f.owner == a && !time.Now().Before(f.retryAt) && (folder == "" || folder == f.folder)
}
func (c *ReclamationCoordinator) permitted(a *mailAccountState, folder string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.permittedLocked(a, folder)
}
func (c *ReclamationCoordinator) retryFolder(a *mailAccountState) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.failureLocked(a); f != nil && f.owner == a {
		return f.folder
	}
	return ""
}
func (c *ReclamationCoordinator) acquire(a *mailAccountState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.permittedLocked(a, "") {
		return false
	}
	found := false
	for _, owner := range c.waiting {
		found = found || owner == a
	}
	if !found {
		c.waiting = append(c.waiting, a)
	}
	for len(c.waiting) > 0 && (c.waiting[0].ctx.Err() != nil || c.waiting[0].parking.Load() || !c.permittedLocked(c.waiting[0], "")) {
		c.waiting = c.waiting[1:]
	}
	if c.active || len(c.waiting) == 0 || c.waiting[0] != a {
		return false
	}
	c.waiting = c.waiting[1:]
	c.active = true
	return true
}
func (c *ReclamationCoordinator) release() { c.mu.Lock(); c.active = false; c.mu.Unlock() }
func (c *ReclamationCoordinator) stage(stage string) {
	c.mu.Lock()
	c.state.Stage = stage
	c.mu.Unlock()
}
func (c *ReclamationCoordinator) pause(reason string, a *mailAccountState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quarantines == nil {
		c.quarantines = map[string]*reclamationFailure{}
	}
	key := reclamationAccountKey(a)
	f := c.quarantines[key]
	if f == nil {
		f = &reclamationFailure{}
		c.quarantines[key] = f
	}
	f.owner, f.folder, f.stage, f.reason = a, c.state.Folder, c.state.Stage, reason
	f.failures = min(f.failures+1, 6)
	f.retryAt = time.Now().Add(time.Second * time.Duration(1<<f.failures))
}
func (c *ReclamationCoordinator) completed(a *mailAccountState, folder string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Completed++
	c.state.Stage = "complete"
	if f := c.failureLocked(a); f != nil && f.owner == a && f.folder == folder {
		delete(c.quarantines, reclamationAccountKey(a))
	}
}
func (c *ReclamationCoordinator) snapshot() ReclamationSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Paused = len(c.quarantines) > 0
	for _, f := range c.quarantines {
		s.Quarantines = append(s.Quarantines, ReclamationQuarantine{f.owner.config.ID, f.folder, f.stage, f.reason, f.retryAt.UnixMilli()})
		if s.RetryAtMS == 0 || f.retryAt.UnixMilli() < s.RetryAtMS {
			s.Failure, s.RetryAtMS = f.reason, f.retryAt.UnixMilli()
			if !c.active {
				s.Stage, s.Folder = f.stage, f.folder
			}
		}
	}
	sort.Slice(s.Quarantines, func(i, j int) bool {
		if s.Quarantines[i].Account != s.Quarantines[j].Account {
			return s.Quarantines[i].Account < s.Quarantines[j].Account
		}
		return s.Quarantines[i].Folder < s.Quarantines[j].Folder
	})
	return s
}
func (c *ReclamationCoordinator) idleLease(a *mailAccountState, cancel context.CancelFunc) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.failureLocked(a)
	// A failed folder in an inactive namespace still needs its fenced audit
	// while another namespace uses this physical account. Ordinary historical
	// sweeps remain deferred; otherwise the retry would be stranded forever.
	retry := f != nil && f.owner == a && f.folder != "" && !time.Now().Before(f.retryAt)
	if len(c.sessions) != 0 && !retry || c.active || !retry && !c.quietSince.IsZero() && time.Since(c.quietSince) < 5*time.Second || !c.permittedLocked(a, "") {
		return false
	}
	c.active = true
	c.idleStop = cancel
	return true
}
func (c *ReclamationCoordinator) endIdle() {
	c.mu.Lock()
	c.active = false
	c.idleStop = nil
	c.mu.Unlock()
}
