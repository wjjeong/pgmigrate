package plan

import (
	"errors"
	"sort"

	"pgmigrate/internal/catalog"
)

// ErrCycle means the selected tables reference each other in a loop.
var ErrCycle = errors.New("FK cycle")

// Order sorts sel parents-first using only FKs between selected tables.
// Self references are ignored; ties are broken by schema.name.
func Order(tables map[catalog.TableRef]*catalog.Table, sel []catalog.TableRef) ([]catalog.TableRef, error) {
	inSel := map[catalog.TableRef]bool{}
	for _, r := range sel {
		inSel[r] = true
	}
	indeg := map[catalog.TableRef]int{}
	children := map[catalog.TableRef][]catalog.TableRef{}
	for r := range inSel {
		indeg[r] += 0
		t := tables[r]
		if t == nil {
			continue
		}
		seen := map[catalog.TableRef]bool{}
		for _, c := range t.Constraints {
			if c.Type != 'f' || c.RefTable == nil {
				continue
			}
			p := *c.RefTable
			if p == r || !inSel[p] || seen[p] {
				continue
			}
			seen[p] = true
			indeg[r]++
			children[p] = append(children[p], r)
		}
	}
	var ready []catalog.TableRef
	for r, d := range indeg {
		if d == 0 {
			ready = append(ready, r)
		}
	}
	var out []catalog.TableRef
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return ready[i].String() < ready[j].String() })
		var next []catalog.TableRef
		for _, cur := range ready {
			out = append(out, cur)
			for _, ch := range children[cur] {
				indeg[ch]--
				if indeg[ch] == 0 {
					next = append(next, ch)
				}
			}
		}
		ready = next
	}
	if len(out) != len(inSel) {
		return nil, ErrCycle
	}
	return out, nil
}
