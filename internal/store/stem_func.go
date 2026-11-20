package store

import (
	"database/sql/driver"
	"fmt"

	"modernc.org/sqlite"

	"tg-cli/internal/stem"
)

// The stemmed full-text index (migration 0011) calls tg_stem() from triggers.
// The driver keeps registered functions in a process-wide table and attaches
// them to every connection it opens afterwards, so registering in init covers
// the write pool, the read pool and the version probe alike. Any other tool
// that writes to messages without this function fails on the triggers; only
// the daemon writes the database.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("tg_stem", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch value := args[0].(type) {
		case nil:
			return "", nil
		case string:
			return stem.Text(value), nil
		case []byte:
			return stem.Text(string(value)), nil
		default:
			return nil, fmt.Errorf("tg_stem: unsupported argument type %T", args[0])
		}
	})
}
