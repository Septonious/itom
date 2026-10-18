package pool

// Fair schedules users before streams. The actor supplies already eligible
// frame sizes (zero means blocked/empty); actual reservation follows atomically
// in that same actor turn. No credit accumulates for an idle or blocked user.
type Fair struct {
	users   []string
	deficit map[string]uint64
	cursor  int
	refill  bool
}

const Quantum uint64 = 128 << 10
const MaxDeficit uint64 = 512 << 10

func NewFair(users []string) (*Fair, error) {
	if len(users) == 0 || len(users) > 1024 {
		return nil, ErrLimit
	}
	f := &Fair{users: append([]string(nil), users...), deficit: map[string]uint64{}, refill: true}
	for _, u := range users {
		if !validUser(u) {
			return nil, ErrScope
		}
		if _, ok := f.deficit[u]; ok {
			return nil, ErrScope
		}
		f.deficit[u] = 0
	}
	return f, nil
}
func (f *Fair) Next(costs map[string]uint64) (string, bool) {
	for visits := 0; visits < 2*len(f.users); visits++ {
		u := f.users[f.cursor]
		cost := costs[u]
		if cost == 0 || cost > 32<<10 {
			f.deficit[u] = 0
			f.cursor = (f.cursor + 1) % len(f.users)
			f.refill = true
			continue
		}
		if f.refill {
			f.deficit[u] += Quantum
			if f.deficit[u] > MaxDeficit {
				f.deficit[u] = MaxDeficit
			}
			f.refill = false
		}
		if f.deficit[u] < cost {
			f.cursor = (f.cursor + 1) % len(f.users)
			f.refill = true
			continue
		}
		f.deficit[u] -= cost
		if f.deficit[u] == 0 {
			f.cursor = (f.cursor + 1) % len(f.users)
			f.refill = true
		}
		return u, true
	}
	return "", false
}
