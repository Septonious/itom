// Package tunnel connects the authenticated protocol, physical journals and TCP
// pump. A lane owns one user/account session; failures fence the entire lane.
package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DATA profiles are an explicit compatibility allowlist. Unknown combinations
// of byte/message limits and foreign owners/folders/providers remain rejected.
var dataProfile8 = pool.Amount{Bytes: 8 << 20, Messages: 128}
var dataProfile12 = pool.Amount{Bytes: 12 << 20, Messages: 192}
var dataProfile16 = pool.Amount{Bytes: 16 << 20, Messages: 256}

func ledgerConfig(a servercfg.Account, u servercfg.User, limit pool.Amount) journal.Config {
	return journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: limit, Users: []journal.User{{ID: u.ID, Limit: limit, Folders: []string{u.Up, u.Down}}}}}}
}

func ledger(dir, name string, a servercfg.Account, u servercfg.User, control bool) (*journal.Store, error) {
	limit := dataProfile12
	var alternatives []journal.Config
	if control {
		limit = pool.Amount{Bytes: 2 << 20, Messages: 128}
	} else {
		for _, capacity := range []pool.Amount{dataProfile8, dataProfile12, dataProfile16} {
			for _, shards := range []int{0, 1, 2, 4} {
				if capacity == limit && shards == 0 {
					continue
				}
				c := ledgerConfig(a, u, capacity)
				c.Accounts[0].Users[0].Folders = topologyFolders(u, shards)
				alternatives = append(alternatives, c)
			}
		}
	}
	s, err := journal.OpenCompatible(filepath.Join(dir, name+".db"), ledgerConfig(a, u, limit), alternatives, time.Second)
	return s, err
}

// MigrateDataProfile is an explicit offline operation, not a side effect of an
// upgrade. The service must be stopped; bbolt rejects a concurrent live owner.
// Atomicity is per DB. An interrupted directory migration can be resumed safely:
// ledger accepts both confirmed profiles and MigrateConfig is idempotent.
func MigrateDataProfile(dir string, a servercfg.Account, u servercfg.User, profile string) error {
	var limit pool.Amount
	switch profile {
	case "data8":
		limit = dataProfile8
	case "data12":
		limit = dataProfile12
	case "data16":
		limit = dataProfile16
	default:
		return journal.ErrConfig
	}
	// Never create a fresh journal as a substitute for the installed state.
	if _, err := os.Stat(filepath.Join(dir, "data.db")); err != nil {
		return err
	}
	s, err := ledger(dir, "data", a, u, false)
	if err != nil {
		return err
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Accounts[0].Limit = limit
	cfg.Accounts[0].Users[0].Limit = limit
	return s.MigrateConfig(cfg)
}

func allAttempts(s *journal.Store) ([]journal.Attempt, error) { return s.ScanAttempts(0, 256) }
func inventory(ctx context.Context, m mailbox.Transport, folder string) ([]mailbox.Message, error) {
	var result []mailbox.Message
	cur := mailbox.Cursor{}
	page := m.Page
	if recovery, ok := m.(interface {
		PageRecovery(context.Context, string, mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error)
	}); ok {
		page = recovery.PageRecovery
	}
	for len(result) <= 512 {
		rows, next, e := page(ctx, folder, cur)
		if e != nil {
			return nil, e
		}
		result = append(result, rows...)
		if next == cur {
			return result, nil
		}
		cur = next
	}
	return nil, errors.New("MAILBOX_INVENTORY_LIMIT")
}
func reclaim(ctx context.Context, m mailbox.Transport, s *journal.Store, rows []mailbox.Message, eligible func(journal.Attempt) bool) ([]journal.Attempt, error) {
	return reclaimBounded(ctx, m, s, rows, eligible, 0)
}

// reclaimBounded preserves reclaim's proof rules while limiting how many
// journal attempts one runtime GC pass may inspect/delete. Startup recovery uses
// limit=0 and remains exhaustive. Runtime cleanup uses one physical batch so a
// single maintenance pass cannot monopolize a public IMAP mailbox for seconds.
func reclaimBounded(ctx context.Context, m mailbox.Transport, s *journal.Store, rows []mailbox.Message, eligible func(journal.Attempt) bool, limit int) ([]journal.Attempt, error) {
	proofs := cleanupProofs{}
	defer proofs.clear()
	return reclaimWithProofs(ctx, m, s, rows, eligible, limit, proofs)
}

func reclaimWithProofs(ctx context.Context, m mailbox.Transport, s *journal.Store, rows []mailbox.Message, eligible func(journal.Attempt) bool, limit int, proofs cleanupProofs) ([]journal.Attempt, error) {
	attempts, e := allAttempts(s)
	if e != nil {
		return nil, e
	}
	type messageKey struct {
		folder string
		digest [32]byte
	}
	byKey := map[messageKey][]mailbox.Message{}
	type pinnedLocator struct {
		folder string
		loc    journal.Location
	}
	byLocation := map[pinnedLocator]mailbox.Message{}
	for _, r := range rows {
		// A decoded digest alone is insufficient physical journal evidence:
		// providers can modify MIME headers while retaining the DATA body.
		// Skip an unprovable physical representation *before* grouping it with
		// valid attempts. Do not delete the message or refund its durable debt;
		// importantly, it cannot poison ObserveMany for unrelated valid UIDs.
		// Parse the physical MIME once for both the bounded journal charge
		// and authenticated-body candidate lookup. This avoids a redundant
		// base64 decode on BINARY/provider-rewritten recovery scans.
		proof := proofs.decode(r.MIME)
		if proof.err != nil {
			continue
		}
		key := messageKey{folder: r.Folder, digest: proof.digest}
		byKey[key] = append(byKey[key], r)
		byLocation[pinnedLocator{r.Folder, journal.Location{UIDValidity: r.Validity, UID: r.UID}}] = r
	}
	for key := range byKey {
		sort.Slice(byKey[key], func(i, j int) bool { return byKey[key][i].UID < byKey[key][j].UID })
	}
	if limit > 0 {
		// A bounded runtime sweep must not repeatedly select old, unprovable
		// attempts while newer messages with valid physical evidence starve.
		// Prefer proof-bearing rows; use the remaining budget for older pinned
		// locators/absence checks. Selection itself NEVER authorizes a refund.
		selected := make(map[journal.AttemptRef]bool, min(limit, len(attempts)))
		for _, a := range attempts {
			if !eligible(a) {
				continue
			}
			rowsWithDigest := byKey[messageKey{folder: a.Folder, digest: a.Digest}]
			if len(rowsWithDigest) == 0 {
				continue
			}
			if a.Location.UID != 0 {
				found := false
				for _, r := range rowsWithDigest {
					if r.UID == a.Location.UID && r.Validity == a.Location.UIDValidity {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			selected[a.Ref] = true
			if len(selected) >= limit {
				break
			}
		}
		if len(selected) < limit {
			for _, a := range attempts {
				if eligible(a) && !selected[a.Ref] {
					selected[a.Ref] = true
					if len(selected) >= limit {
						break
					}
				}
			}
		}
		eligible = func(a journal.Attempt) bool { return selected[a.Ref] }
	}
	used := map[pinnedLocator]bool{}
	var removed []journal.Attempt
	var candidates []journal.Attempt
	var messages []mailbox.Message
	var observations []journal.ObservedAttempt
	var provenAbsent []journal.Attempt
	inventoryValidities := map[string]uint32{}
	for _, r := range rows {
		if r.Validity == 0 {
			continue
		}
		if inventoryValidities[r.Folder] == 0 {
			inventoryValidities[r.Folder] = r.Validity
		}
	}

	// Runtime pages are partial inventories. Probe missing pinned locators in
	// bounded exact UID sets, preserving the same UIDVALIDITY absence proof
	// without serializing one provider round trip per journal attempt.

	presence := map[pinnedLocator]bool{}
	presenceChecked := map[pinnedLocator]bool{}
	if batch, ok := m.(interface {
		Present(context.Context, string, uint32, []uint32) (map[uint32]bool, error)
	}); ok && limit > 0 {
		type scope struct {
			folder   string
			validity uint32
		}
		groups := map[scope][]uint32{}
		for _, a := range attempts {
			if !eligible(a) || a.State != journal.Committed || a.Location.UID == 0 || inventoryValidities[a.Folder] == 0 || a.Location.UIDValidity != inventoryValidities[a.Folder] {
				continue
			}
			if _, found := byLocation[pinnedLocator{a.Folder, a.Location}]; found {
				continue
			}
			key := scope{a.Folder, a.Location.UIDValidity}
			groups[key] = append(groups[key], a.Location.UID)
		}
		for key, ids := range groups {
			found, err := batch.Present(ctx, key.folder, key.validity, ids)
			if errors.Is(err, mailbox.ErrChanged) {
				for _, uid := range ids {
					loc := pinnedLocator{key.folder, journal.Location{UIDValidity: key.validity, UID: uid}}
					presenceChecked[loc], presence[loc] = true, true
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, uid := range ids {
				loc := pinnedLocator{key.folder, journal.Location{UIDValidity: key.validity, UID: uid}}
				presenceChecked[loc] = true
				presence[loc] = found[uid]
			}
		}
	}

	// Preserve an already-observed locator before considering digest matching.
	// A physical UID can prove at most one attempt. This is critical when several
	// uncertain APPENDs carry identical plaintext/digests: observing one copy must
	// never authorize deletion/refund of every matching journal attempt.
	for _, a := range attempts {
		if !eligible(a) || a.State == journal.Prepared || a.Location.UID == 0 {
			continue
		}
		loc := a.Location
		if r, ok := byLocation[pinnedLocator{a.Folder, loc}]; ok && r.Folder == a.Folder && !used[pinnedLocator{a.Folder, loc}] {
			proof := proofs.decode(r.MIME)
			if proof.err == nil && proof.digest == a.Digest {
				observations = append(observations, journal.ObservedAttempt{Ref: a.Ref, Observation: journal.Observation{Account: a.Ref.Grant.Scope.Account, Folder: r.Folder, EncodedBytes: proof.encoded, Location: loc, Decoded: proof.body}})
				used[pinnedLocator{a.Folder, loc}] = true
				candidates = append(candidates, a)
				messages = append(messages, r)
				continue
			}
		}
		if a.State == journal.Committed {
			// A complete inventory already tells us which mailbox incarnation is
			// current. Never probe a locator from an older UIDVALIDITY: providers may
			// answer such FETCH/absence requests inconsistently after folder recreation.
			// Empty inventories are handled by reconcileEmptyLocalFolder below.
			if inventoryValidities[a.Folder] == 0 || loc.UIDValidity != inventoryValidities[a.Folder] {
				continue
			}
			absent, absentErr := false, error(nil)
			if presenceChecked[pinnedLocator{a.Folder, loc}] {
				absent = !presence[pinnedLocator{a.Folder, loc}]
			} else {
				absent, absentErr = m.Absent(ctx, a.Folder, loc.UIDValidity, loc.UID)
			}
			if errors.Is(absentErr, mailbox.ErrChanged) {
				// A provider-side folder recreation/UIDVALIDITY rotation invalidates
				// the locator but is not proof that this physical attempt vanished.
				// Keep the conservative debt and continue recovering the lane instead
				// of permanently reconnect-looping on the stale locator. A later full
				// empty inventory (or an exact digest observation) can still prove it.
				continue
			}
			if absentErr != nil {
				return nil, absentErr
			}
			if absent {
				provenAbsent = append(provenAbsent, a)
			}
		}
	}
	for start := 0; start < len(provenAbsent); start += journal.MaxProofBatch {
		chunk := provenAbsent[start:min(start+journal.MaxProofBatch, len(provenAbsent))]
		proofs := make([]journal.Deletion, len(chunk))
		for i, a := range chunk {
			proofs[i] = journal.Deletion{Ref: a.Ref, Location: a.Location}
		}
		if e = s.DeletedMany(s.Run(), proofs); e != nil {
			return nil, e
		}
		removed = append(removed, chunk...)
	}

	for _, a := range attempts {
		if !eligible(a) {
			continue
		}
		if a.State == journal.Prepared {
			if e = s.CancelPrepared(a.Ref); e != nil {
				return nil, e
			}
			continue
		}
		if a.Location.UID != 0 {
			continue // pinned/handled above; never remap an observed attempt.
		}
		key := messageKey{folder: a.Folder, digest: a.Digest}
		queue := byKey[key]
		var r mailbox.Message
		found := false
		for len(queue) > 0 {
			r = queue[0]
			queue = queue[1:]
			loc := journal.Location{UIDValidity: r.Validity, UID: r.UID}
			if !used[pinnedLocator{a.Folder, loc}] {
				used[pinnedLocator{a.Folder, loc}] = true
				found = true
				break
			}
		}
		byKey[key] = queue
		if !found {
			continue
		}
		proof := proofs.decode(r.MIME)
		if proof.err != nil {
			return nil, proof.err
		}
		loc := journal.Location{UIDValidity: r.Validity, UID: r.UID}
		observations = append(observations, journal.ObservedAttempt{Ref: a.Ref, Observation: journal.Observation{Account: a.Ref.Grant.Scope.Account, Folder: r.Folder, EncodedBytes: proof.encoded, Location: loc, Decoded: proof.body}})
		candidates = append(candidates, a)
		messages = append(messages, r)
	}

	// All matched locators must be durable before any physical DELETE. A
	// provider may rewrite an old MIME representation, or a legacy descriptor
	// may have charged a different byte count while retaining the decoded DATA
	// digest. ObserveMany deliberately fails atomically with JOURNAL_SCOPE for
	// even one invalid proof. Such a proof must NOT reconnect-loop every line,
	// and must NOT prevent unrelated valid UIDs from being cleaned up.
	//
	// Keep the fast batched path for normal mailboxes. On a scope mismatch,
	// replay the unchanged evidence individually: accept only proofs validated
	// by the journal, leave every unprovable physical message and its durable
	// debt untouched. Other errors (I/O, generation, corruption, state) remain
	// fatal. In particular, a failure must never become an absence/refund proof.
	verifiedCandidates := make([]journal.Attempt, 0, len(candidates))
	verifiedMessages := make([]mailbox.Message, 0, len(messages))
	for start := 0; start < len(observations); start += journal.MaxProofBatch {
		end := min(start+journal.MaxProofBatch, len(observations))
		if e = s.ObserveMany(s.Run(), observations[start:end]); e == nil {
			verifiedCandidates = append(verifiedCandidates, candidates[start:end]...)
			verifiedMessages = append(verifiedMessages, messages[start:end]...)
			continue
		}
		if !errors.Is(e, journal.ErrScope) {
			return nil, e
		}
		for i := start; i < end; i++ {
			proof := observations[i]
			if oneErr := s.Observe(proof.Ref, s.Run(), proof.Observation); oneErr != nil {
				if errors.Is(oneErr, journal.ErrScope) {
					continue // Unproven physical evidence: preserve mailbox + debt.
				}
				return nil, oneErr
			}
			verifiedCandidates = append(verifiedCandidates, candidates[i])
			verifiedMessages = append(verifiedMessages, messages[i])
		}
	}
	candidates, messages = verifiedCandidates, verifiedMessages
	// Provider batch APIs operate in one SELECTed folder. Keep each durable
	// attempt paired with its exact locator while grouping physical deletion.
	order := make([]int, len(messages))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := messages[order[i]], messages[order[j]]
		if a.Folder != b.Folder {
			return a.Folder < b.Folder
		}
		return a.Validity < b.Validity
	})
	sortedCandidates := make([]journal.Attempt, len(order))
	sortedMessages := make([]mailbox.Message, len(order))
	for i, n := range order {
		sortedCandidates[i] = candidates[n]
		sortedMessages[i] = messages[n]
	}
	candidates, messages = sortedCandidates, sortedMessages
	// Each bounded delete batch contains only already committed exact proofs.
	for start := 0; start < len(messages); {
		end := start + 1
		for end < len(messages) && end-start < journal.MaxProofBatch && messages[end].Folder == messages[start].Folder && messages[end].Validity == messages[start].Validity {
			end++
		}
		chunk := messages[start:end]
		if observed, ok := m.(interface {
			DeleteObservedMany(context.Context, []mailbox.Message) error
		}); ok {
			// Every message in chunk was decoded, digest-matched and bound to the
			// durable attempt above. UIDPLUS can therefore delete the exact observed
			// locator without fetching the same immutable body a second time.
			if e = observed.DeleteObservedMany(ctx, chunk); e != nil {
				return nil, e
			}
		} else if batch, ok := m.(interface {
			DeleteMany(context.Context, []mailbox.Message) error
		}); ok {
			if e = batch.DeleteMany(ctx, chunk); e != nil {
				return nil, e
			}
		} else {
			for _, msg := range chunk {
				if e = m.Delete(ctx, msg); e != nil {
					return nil, e
				}
			}
		}
		proofs := make([]journal.Deletion, len(chunk))
		for i, msg := range chunk {
			proofs[i] = journal.Deletion{Ref: candidates[start+i].Ref, Location: journal.Location{UIDValidity: msg.Validity, UID: msg.UID}}
		}
		if e = s.DeletedMany(s.Run(), proofs); e != nil {
			return nil, e
		}
		removed = append(removed, candidates[start:end]...)
		start = end
	}
	return removed, nil
}

func deleteMailboxRows(ctx context.Context, m mailbox.Transport, rows []mailbox.Message) error {
	for start := 0; start < len(rows); start += 32 {
		end := min(start+32, len(rows))
		chunk := rows[start:end]
		if batch, ok := m.(interface {
			DeleteMany(context.Context, []mailbox.Message) error
		}); ok {
			if err := batch.DeleteMany(ctx, chunk); err != nil {
				return err
			}
		} else {
			for _, msg := range chunk {
				if err := m.Delete(ctx, msg); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var legacyPreauthMagics = [][]byte{
	[]byte("ITP8"), []byte("ITC8"), []byte("ITH8"),
	[]byte("ITP9"), []byte("ITC9"), []byte("ITH9"),
	[]byte("ITP10"), []byte("ITC10"), []byte("ITH10"),
}

func isPreauthBody(body []byte) bool {
	for _, magic := range legacyPreauthMagics {
		if bytes.HasPrefix(body, magic) {
			return true
		}
	}
	return false
}

func stalePreauthRows(rows []mailbox.Message) []mailbox.Message {
	out := make([]mailbox.Message, 0, len(rows))
	for _, row := range rows {
		body, err := mailbox.Decode(row.MIME)
		if err != nil {
			continue
		}
		if isPreauthBody(body) {
			out = append(out, row)
		}
	}
	return out
}

// reconcileEmptyLocalFolder is used only after inventory() has completed
// successfully. It first removes stale unjournaled pre-auth traffic left by a
// crashed lane, then, if the folder is physically empty, releases conservative
// LOCAL physical attempts that can no longer consume mailbox storage.
func reconcileEmptyLocalFolder(ctx context.Context, m mailbox.Transport, s *journal.Store, folder string) error {
	rows, err := inventory(ctx, m, folder)
	if err != nil {
		return err
	}
	stale := stalePreauthRows(rows)
	if len(stale) != 0 {
		if err = deleteMailboxRows(ctx, m, stale); err != nil {
			return err
		}
		rows, err = inventory(ctx, m, folder)
		if err != nil {
			return err
		}
	}
	if len(rows) != 0 {
		return nil
	}
	attempts, err := allAttempts(s)
	if err != nil {
		return err
	}
	for _, a := range attempts {
		if a.Ordinal != 0 || a.Folder != folder || a.State == journal.Prepared {
			continue
		}
		if err = s.AbsentFromCompleteInventory(a.Ref, s.Run(), folder); err != nil {
			return err
		}
	}
	return nil
}

func appendEphemeralControl(ctx context.Context, m mailbox.Transport, folder string, body []byte) error {
	raw, err := mailbox.Encode(body)
	if err != nil {
		return err
	}
	defer clear(raw)
	if split, ok := m.(interface {
		AppendControl(context.Context, string, []byte) error
	}); ok {
		return split.AppendControl(ctx, folder, raw)
	}
	return m.Append(ctx, folder, raw)
}

func recoverLocal(ctx context.Context, m mailbox.Transport, s *journal.Store, folder string) ([]journal.AttemptRef, error) {
	before, e := allAttempts(s)
	if e != nil {
		return nil, e
	}
	// No LOCAL attempts means there is no physical local debt to recover.
	// Remote reservations remain charged; the authenticated reader still
	// processes mailbox contents. Never skip an inventory for unknown APPEND.
	needsInventory := false
	for _, a := range before {
		if a.Ordinal == 0 && a.Folder == folder {
			needsInventory = true
			break
		}
	}
	if needsInventory {
		rows, err := inventory(ctx, m, folder)
		if err != nil {
			return nil, err
		}
		if _, err = reclaim(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal == 0 }); err != nil {
			return nil, err
		}
		if err = reconcileEmptyLocalFolder(ctx, m, s, folder); err != nil {
			return nil, err
		}
	}
	grants, e := s.ScanGrants(0, 256)
	if e != nil {
		return nil, e
	}
	for _, g := range grants {
		if !g.Remote && !g.Closed {
			if e = s.CloseGrant(g.Ref); e != nil {
				return nil, e
			}
		}
	}
	after, e := allAttempts(s)
	if e != nil {
		return nil, e
	}
	live := make(map[journal.AttemptRef]struct{}, len(after))
	for _, a := range after {
		live[a.Ref] = struct{}{}
	}
	recovered := make([]journal.AttemptRef, 0)
	for _, a := range before {
		if a.Ordinal != 0 || a.Folder != folder {
			continue
		}
		if _, ok := live[a.Ref]; !ok {
			recovered = append(recovered, a.Ref)
		}
	}
	return recovered, nil
}
func appendDurableVia(ctx context.Context, s *journal.Store, scope journal.Scope, folder string, body []byte, appendFn func(context.Context, string, []byte) error) (journal.AttemptRef, error) {
	raw, e := mailbox.Encode(body)
	if e != nil {
		return journal.AttemptRef{}, e
	}
	defer clear(raw)
	if err := ctx.Err(); err != nil {
		return journal.AttemptRef{}, err
	}
	var batch wire.ID
	if _, e = rand.Read(batch[:]); e != nil {
		return journal.AttemptRef{}, e
	}
	a, e := s.StartLocalAttempt(scope, folder, batch, uint64(len(raw)), body)
	if e != nil {
		return journal.AttemptRef{}, e
	}
	if err := ctx.Err(); err != nil {
		// No APPEND was invoked, so this single writer can prove rejection.
		return a.Ref, errors.Join(err, s.Complete(a.Ref, journal.RejectedBeforeCommit))
	}
	e = appendFn(ctx, folder, raw)
	out := journal.Accepted
	if e != nil {
		out = journal.Uncertain
	}
	return a.Ref, errors.Join(e, s.Complete(a.Ref, out))
}
func appendDurable(ctx context.Context, m mailbox.Transport, s *journal.Store, scope journal.Scope, folder string, body []byte) (journal.AttemptRef, error) {
	return appendDurableVia(ctx, s, scope, folder, body, m.Append)
}
func appendDurableControl(ctx context.Context, m mailbox.Transport, s *journal.Store, scope journal.Scope, folder string, body []byte) (journal.AttemptRef, error) {
	if split, ok := m.(interface {
		AppendControl(context.Context, string, []byte) error
	}); ok {
		return appendDurableVia(ctx, s, scope, folder, body, split.AppendControl)
	}
	return appendDurable(ctx, m, s, scope, folder, body)
}
func appendData(ctx context.Context, m mailbox.Transport, folder string, raw []byte, fast bool) error {
	if split, ok := m.(interface {
		AppendDataPriority(context.Context, string, []byte, bool) error
	}); ok {
		return split.AppendDataPriority(ctx, folder, raw, fast)
	}
	if split, ok := m.(interface {
		AppendData(context.Context, string, []byte) error
	}); ok {
		return split.AppendData(ctx, folder, raw)
	}
	return m.Append(ctx, folder, raw)
}
func dataParallelism(m mailbox.Transport) int {
	if split, ok := m.(interface{ DataParallelism() int }); ok {
		n := split.DataParallelism()
		if n > 1 && n <= 4 {
			return n
		}
	}
	return 1
}
func sortMessages(rows []mailbox.Message) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].UID < rows[j].UID })
}
func grantRef(k wire.QuotaKey) journal.GrantRef {
	return journal.GrantRef{Store: k.Store, ID: k.Grant, Scope: journal.Scope{Account: k.Account, User: k.User, Session: k.Session}}
}

// Invalid MIME returns zero; journal Observe then rejects it as evidence.
func canonicalMessageSize(raw []byte) uint64 {
	n, err := mailbox.CanonicalEncodedBytes(raw)
	if err != nil {
		return 0
	}
	return n
}
