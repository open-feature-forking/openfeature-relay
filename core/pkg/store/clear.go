package store

import (
	"fmt"

	"github.com/hashicorp/go-memdb"
)

// Clear removes every flag and every document in one transaction: a reader sees the store as it was or empty, never
// half emptied. The registered sources stay, so that deliveries are accepted again as they were at start.
func (s *Store) Clear() {
	txn := s.db.Txn(true)
	defer txn.Abort()

	for _, table := range []string{flagsTable, documentsTable} {
		if err := deleteAll(txn, table); err != nil {
			// the store has no failing path for a delete of rows it holds: leave it unchanged and say so
			s.logger.Error(fmt.Sprintf("unable to clear the %s table: %v", table, err))
			return
		}
	}
	txn.Commit()
}

func deleteAll(txn *memdb.Txn, table string) error {
	it, err := txn.Get(table, idIndex)
	if err != nil {
		return err
	}
	var rows []any
	for raw := it.Next(); raw != nil; raw = it.Next() {
		rows = append(rows, raw)
	}
	for _, row := range rows {
		if err := txn.Delete(table, row); err != nil {
			return err
		}
	}
	return nil
}
