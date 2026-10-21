package plan

import (
	"context"
	"errors"
	"fmt"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
)

// Gather reads the source and target state needed by Check.
func Gather(ctx context.Context, src, tgt catalog.Querier, sel []Selection) (*Facts, error) {
	f := &Facts{
		Source:     map[catalog.TableRef]*catalog.Table{},
		SourceRows: map[catalog.TableRef]int64{},
		Target:     map[catalog.TableRef]*TargetFacts{},
	}
	inSel := map[catalog.TableRef]bool{}
	for _, s := range sel {
		inSel[s.Ref] = true
	}
	for _, s := range sel {
		ref := s.Ref
		st, err := catalog.Describe(ctx, src, ref)
		if err != nil {
			return nil, fmt.Errorf("소스 테이블 %s 구조 조회 실패: %w", ref, err)
		}
		f.Source[ref] = st
		if f.SourceRows[ref], err = catalog.CountRows(ctx, src, ref); err != nil {
			return nil, err
		}
		tf, err := gatherTarget(ctx, tgt, s, st, inSel)
		if err != nil {
			return nil, err
		}
		f.Target[ref] = tf
	}
	return f, nil
}

func gatherTarget(ctx context.Context, tgt catalog.Querier, s Selection, st *catalog.Table, inSel map[catalog.TableRef]bool) (*TargetFacts, error) {
	ref := s.Ref
	tf := &TargetFacts{}
	var err error
	if tf.SchemaExists, err = catalog.SchemaExists(ctx, tgt, ref.Schema); err != nil {
		return nil, err
	}
	if tf.SchemaExists {
		if tf.SchemaCreate, err = catalog.SchemaCreatePrivilege(ctx, tgt, ref.Schema); err != nil {
			return nil, err
		}
	}
	tt, err := catalog.Describe(ctx, tgt, ref)
	switch {
	case errors.Is(err, catalog.ErrNotFound):
	case err != nil:
		return nil, fmt.Errorf("타깃 테이블 %s 구조 조회 실패: %w", ref, err)
	default:
		tf.Table = tt
	}
	if tt != nil {
		if tf.Priv, err = catalog.TablePrivileges(ctx, tgt, ref); err != nil {
			return nil, err
		}
		if tf.ReferencedBy, err = catalog.ReferencingTables(ctx, tgt, ref); err != nil {
			return nil, err
		}
		if tf.DependentViews, err = catalog.DependentViews(ctx, tgt, ref); err != nil {
			return nil, err
		}
		if tf.Rows, err = catalog.CountRows(ctx, tgt, ref); err != nil {
			return nil, err
		}
		if s.Mode != ModeCreate {
			tf.SeqUpdate = map[catalog.TableRef]bool{}
			for _, q := range tt.Sequences {
				if tf.SeqUpdate[q.Ref], err = catalog.SequenceUpdatePrivilege(ctx, tgt, q.Ref); err != nil {
					return nil, err
				}
			}
		}
	}
	if s.Mode == ModeCreate {
		seen := map[string]bool{}
		for _, c := range st.Columns {
			if !c.UserType {
				continue
			}
			name := c.TypeSchema + "." + c.TypeName
			if seen[name] {
				continue
			}
			seen[name] = true
			ok, err := catalog.TypeExists(ctx, tgt, c.TypeSchema, c.TypeName)
			if err != nil {
				return nil, err
			}
			if !ok {
				tf.MissingTypes = append(tf.MissingTypes, name)
			}
		}
		seenRef := map[catalog.TableRef]bool{}
		for _, c := range st.Constraints {
			if c.Type != 'f' || c.RefTable == nil {
				continue
			}
			r := *c.RefTable
			if r == ref || inSel[r] || seenRef[r] {
				continue
			}
			seenRef[r] = true
			ok, err := catalog.TableExists(ctx, tgt, r)
			if err != nil {
				return nil, err
			}
			if !ok {
				tf.MissingFKTables = append(tf.MissingFKTables, r)
			}
		}
	}
	return tf, nil
}

// Build gathers the facts and checks them.
func Build(ctx context.Context, src, tgt catalog.Querier, srcConn, tgtConn config.Conn, sel []Selection) (*Plan, error) {
	f, err := Gather(ctx, src, tgt, sel)
	if err != nil {
		return nil, err
	}
	return Check(srcConn, tgtConn, sel, f), nil
}
