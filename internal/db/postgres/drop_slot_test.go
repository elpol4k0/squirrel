package postgres

import (
	"testing"
)

// integration covered by testcontainers tests
func TestAdapter_DropSlot_MethodExists(t *testing.T) {
	a := New("postgres://user:pw@localhost/db?replication=database")
	var _ func(interface{}, string) error
	_ = a.DropSlot // assigning to blank identifier ensures the symbol resolves
}
