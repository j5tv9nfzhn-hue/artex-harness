package evidence

import (
	"context"
	"os"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// Initialize an explicitly configured fresh database before taking the same
// suite lock as db, agent and server. Hold it on one pinned connection.
func TestMain(m *testing.M) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		// 破坏性用例必须显式指名一个测试库：这些 suite 会 TRUNCATE 真实表，
		// 早先它们与开发服务器共用同一个库，一次 go test 就把在用的 LLM profile 删了。
		if ok, why := db.TestDatabaseAllowed(dsn, "ARTEX_TEST_DB"); !ok {
			os.Stderr.WriteString("[test] skipped destructive DB suite: " + why + "\n")
			os.Exit(0)
		}
		os.Exit(m.Run())
	}
	os.Exit(runEvidenceSuite(m))
}
func runEvidenceSuite(m *testing.M) int {
	pg, err := db.Open(os.Getenv("ARTEX_PG_DSN"))
	if err != nil {
		panic(err)
	}
	defer pg.Close()
	conn, err := pg.Conn(context.Background())
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(7337741002)`); err != nil {
		panic(err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7337741002)`)
	return m.Run()
}
