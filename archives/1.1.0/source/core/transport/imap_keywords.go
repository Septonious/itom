package transport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrKeywordsUnsupported = errors.New("V2_IMAP_PERMANENT_KEYWORDS_REQUIRED")

func (e *Executor) KeywordsSupported() bool { return e.keywords }

func validKeyword(keyword string) bool {
	if len(keyword) == 0 || len(keyword) > 128 {
		return false
	}
	for _, c := range []byte(keyword) {
		if c <= 32 || c >= 127 || strings.ContainsRune(`(){%*"\]`, rune(c)) {
			return false
		}
	}
	return true
}

// StoreKeyword changes bounded metadata on one existing actual message. It
// cannot APPEND, copy a message, issue a grant, authenticate a peer or authorize
// quota release. A rendezvous request is only a MAC-verified routing hint for
// the issuer; any resulting grant still needs original durable admission.
func (e *Executor) StoreKeyword(ctx context.Context, uid uint32, keyword string, add bool) (*Command, error) {
	if !e.keywords {
		return nil, ErrKeywordsUnsupported
	}
	if uid == 0 || !validKeyword(keyword) {
		return nil, ErrProtocol
	}
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tag, op, err := e.begin(commandStore, false, false)
	if err != nil {
		return nil, err
	}
	sign := "-"
	if add {
		sign = "+"
	}
	if err = e.write([]byte(fmt.Sprintf("%s UID STORE %d %sFLAGS.SILENT (%s)\r\n", tag, uid, sign, keyword))); err != nil {
		e.fail(ErrUnknown)
		return &Command{op}, ErrUnknown
	}
	return &Command{op}, nil
}

func (e *Executor) FetchFlags(ctx context.Context, uid uint32) (*Command, error) {
	if uid == 0 {
		return nil, ErrProtocol
	}
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tag, op, err := e.begin(commandFlags, true, false)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	op.first, op.last, op.flags = uid, uid, true
	op.uids = make(map[uint32]bool)
	e.mu.Unlock()
	if err = e.write([]byte(fmt.Sprintf("%s UID FETCH %d:%d (UID FLAGS)\r\n", tag, uid, uid))); err != nil {
		e.fail(ErrUnknown)
		return &Command{op}, ErrUnknown
	}
	return &Command{op}, nil
}

// Parse only the requested UID/FLAGS form. Unknown structures and response
// overflow fail closed under the same command bounds as BODY/inventory reads.
func (e *Executor) readFlags(line string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op := e.fetch
	if op == nil || !op.flags {
		return false, nil
	}
	sequence, err := fetchSequence(line)
	if err != nil {
		return false, err
	}
	if len(line)+2 > e.limits.ResponseBytes-op.bytes {
		return false, ErrCapacity
	}
	parts := strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(line))
	if len(parts) < 7 || parts[3] != "(" || parts[len(parts)-1] != ")" {
		return false, ErrProtocol
	}
	var uid uint32
	var flags []string
	seenFlags := false
	for i := 4; i < len(parts)-1; {
		switch strings.ToUpper(parts[i]) {
		case "UID":
			if uid != 0 || i+1 >= len(parts)-1 {
				return false, ErrProtocol
			}
			n, err := strconv.ParseUint(parts[i+1], 10, 32)
			if err != nil || n == 0 {
				return false, ErrProtocol
			}
			uid, i = uint32(n), i+2
		case "FLAGS":
			if seenFlags || i+1 >= len(parts)-1 || parts[i+1] != "(" {
				return false, ErrProtocol
			}
			seenFlags, i = true, i+2
			for i < len(parts)-1 && parts[i] != ")" {
				flag := parts[i]
				atom := strings.TrimPrefix(flag, `\`)
				if !validKeyword(atom) || len(flags) >= 64 {
					return false, ErrCapacity
				}
				flags = append(flags, flag)
				i++
			}
			if i >= len(parts)-1 {
				return false, ErrProtocol
			}
			i++
		default:
			return false, ErrProtocol
		}
	}
	if uid == 0 && seenFlags || uid != 0 && !seenFlags {
		return false, nil // legal unsolicited FLAGS-only or UID-only notice
	}
	if uid == 0 || !seenFlags {
		return false, ErrProtocol
	}
	if uid != op.first {
		return false, nil // legal unsolicited update for another actual UID
	}
	op.bytes += len(line) + 2
	if op.uids[uid] {
		// A later unsolicited FLAGS update for this same UID can precede the
		// tag. Preserve one bounded latest hint, not a second BODY/proof.
		op.result.Bodies[0] = Body{UID: uid, Sequence: sequence, Flags: flags}
		return true, nil
	}
	op.uids[uid] = true
	op.result.Bodies = append(op.result.Bodies, Body{UID: uid, Sequence: sequence, Flags: flags})
	return true, nil
}
