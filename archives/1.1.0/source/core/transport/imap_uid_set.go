package transport

import (
	"context"
	"fmt"
	"itom.local/core/journal"
	"sort"
	"strconv"
	"strings"
)

// UID sets are explicit, copied and bounded by the original proof transaction
// limit. No range expansion can accidentally mark/expunge an intervening UID.
func checkedUIDSet(input []uint32) ([]uint32, string, error) {
	if len(input) == 0 || len(input) > journal.MaxProofBatch {
		return nil, "", ErrCapacity
	}
	uids := append([]uint32(nil), input...)
	sort.Slice(uids, func(a, b int) bool { return uids[a] < uids[b] })
	parts := make([]string, len(uids))
	for n, uid := range uids {
		if uid == 0 || n > 0 && uid == uids[n-1] {
			return nil, "", ErrProtocol
		}
		parts[n] = strconv.FormatUint(uint64(uid), 10)
	}
	return uids, strings.Join(parts, ","), nil
}

func (e *Executor) fetchUIDSet(ctx context.Context, input []uint32, flags bool) (*Command, error) {
	return e.fetchExact(ctx, input, flags, false, false)
}

// fetchBodies requests only a bounded, explicitly discovered set. The ordinary
// literal and aggregate response limits still constrain the single parser.
func (e *Executor) fetchBodies(ctx context.Context, input []uint32) (*Command, error) {
	return e.fetchExact(ctx, input, false, true, true)
}

func (e *Executor) fetchMaintenance(ctx context.Context, input []uint32, flags bool) (*Command, error) {
	return e.fetchExact(ctx, input, flags, false, true)
}

func (e *Executor) fetchExact(ctx context.Context, input []uint32, flags, bodies, queued bool) (*Command, error) {
	uids, set, err := checkedUIDSet(input)
	if err != nil {
		return nil, err
	}
	if len(uids) == 1 {
		set = fmt.Sprintf("%d:%d", uids[0], uids[0])
	}
	var turnOp *operation
	if queued {
		finish, err := e.acquireFetchTurn(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { finish(turnOp) }()
	}
	if err = e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	class, items := commandInventory, "UID"
	if flags {
		class, items = commandFlags, "UID FLAGS"
	}
	if bodies {
		class, items = commandBody, "UID BODY.PEEK[]"
	}
	tag, op, err := e.begin(class, true, false)
	if err != nil {
		return nil, err
	}
	turnOp = op
	e.mu.Lock()
	op.first, op.last = uids[0], uids[len(uids)-1]
	op.flags, op.metadata = flags, !flags && !bodies
	op.uids = make(map[uint32]bool)
	op.requested = make(map[uint32]bool, len(uids))
	for _, uid := range uids {
		op.requested[uid] = true
	}
	e.mu.Unlock()
	if err = e.write([]byte(fmt.Sprintf("%s UID FETCH %s (%s)\r\n", tag, set, items))); err != nil {
		e.fail(ErrUnknown)
		return &Command{op}, ErrUnknown
	}
	return &Command{op}, nil
}

func (op *operation) requestsUID(uid uint32) bool {
	return uid >= op.first && uid <= op.last && (op.requested == nil || op.requested[uid])
}
