package store

import (
	"database/sql/driver"
	"io"
)

type updaterPolicyAtomicRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *updaterPolicyAtomicRows) Columns() []string { return r.columns }
func (r *updaterPolicyAtomicRows) Close() error      { return nil }
func (r *updaterPolicyAtomicRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
