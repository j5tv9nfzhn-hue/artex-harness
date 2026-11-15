package db

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMain acquires a PostgreSQL advisory lock (7337741002) for the entire
// db test suite. Packages agent and server hold the same lock, so parallel
// `go test ./...` runs serialize across packages on the shared dev DB and
// avoid cross-package cleanup races (e.g. DELETE FROM assets WHERE id > X
// from one package deleting assets created by another).
func TestMain(m *testing.M) {
	dsn, _, err := DSN()
	if err != nil {
		// No DB configured — tests that need PG will skip themselves.
		os.Exit(m.Run())
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil || conn.Ping() != nil {
		os.Exit(m.Run())
	}
	defer conn.Close()
	if _, err := conn.Exec(`SELECT pg_advisory_lock(7337741002)`); err != nil {
		os.Exit(m.Run())
	}
	defer conn.Exec(`SELECT pg_advisory_unlock(7337741002)`) //nolint:errcheck
	// 破坏性用例必须显式指名一个测试库：这些 suite 会 TRUNCATE 真实表，
	// 早先它们与开发服务器共用同一个库，一次 go test 就把在用的 LLM profile 删了。
	if ok, why := TestDatabaseAllowed(dsn, "ARTEX_TEST_DB"); !ok {
		os.Stderr.WriteString("[test] skipped destructive DB suite: " + why + "\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
