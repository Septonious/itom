package transport

import (
	"context"
	"sort"
	"strings"
)

// Fragmentation changes only representation of the original 16-byte Session
// and full 32-byte MAC. No fragment/hash/provider flag is allocation authority.
// Supported name lengths are 59, 31 and 16; all fit the original FLAGS bounds.
func requestFragments(keyword string, width int) []string {
	if len(keyword) != 103 || !strings.EqualFold(keyword[:6], "ITOM2_") || keyword[38] != '_' {
		return nil
	}
	return keywordFragments(keyword[6:38]+keyword[39:], 'i', width)
}

type rendezvousRequest struct {
	keyword string
	flags   []string
}

// RequestKeywords authenticates only complete requests. Casing, ordering,
// duplicate fragments and multiple representations do not allocate another
// Session. Partial, mixed, conflicting or forged groups cannot reach admission.
func (v *VerifiedRendezvous) RequestKeywords(flags []string, key []byte) []string {
	var keywords []string
	for _, request := range v.requests(flags, key) {
		keywords = append(keywords, request.keyword)
	}
	return keywords
}

func (v *VerifiedRendezvous) requests(flags []string, key []byte) []rendezvousRequest {
	if len(flags) > 64 {
		return nil
	}
	hints := map[string][]string{}
	for _, flag := range flags {
		lower := strings.ToLower(flag)
		if len(lower) == 103 && strings.HasPrefix(lower, "itom2_") {
			hints[lower] = append(hints[lower], flag)
		}
	}
	for payload, physical := range decodeKeywordFragments(flags, 'i', 96) {
		keyword := "itom2_" + payload[:32] + "_" + payload[32:]
		hints[keyword] = append(hints[keyword], physical...)
	}
	var result []rendezvousRequest
	for keyword, physical := range hints {
		if _, err := v.RequestTemplate(keyword, key); err == nil {
			result = append(result, rendezvousRequest{keyword: keyword, flags: physical})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].keyword < result[j].keyword })
	return result
}

func (v *VerifiedRendezvous) clearRequest(ctx context.Context, x *Executor, request rendezvousRequest) error {
	var flags []string
	seen := map[string]bool{}
	for _, flag := range request.flags {
		canonical := strings.ToLower(flag)
		if !seen[canonical] {
			seen[canonical] = true
			flags = append(flags, flag)
		}
	}
	cmd, err := x.storeKeywords(ctx, v.uid, flags, false)
	if err == nil {
		_, err = cmd.Wait(ctx)
	}
	return err
}
