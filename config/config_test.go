package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathPrefersHarnessConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HARNESS_CONFIG", "")
	t.Setenv("ARTEX_CONFIG", "")

	// 只有旧 config.json 时，兼容回退仍能找到它。
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"database":{"dbname":"legacy"}}`), 0o644)
	if got := Path(); got != filepath.Join(dir, "config.json") {
		t.Fatalf("legacy fallback: got %q", got)
	}
	// 两者都存在时，artex-harness.json 优先——与上游 ARTEX 同目录解压也不共用配置。
	os.WriteFile(filepath.Join(dir, "artex-harness.json"), []byte(`{"database":{"dbname":"artex_harness"}}`), 0o644)
	if got := Path(); got != filepath.Join(dir, "artex-harness.json") {
		t.Fatalf("harness config should win: got %q", got)
	}
	// 都不存在时，默认指向待创建的 artex-harness.json。
	os.Remove(filepath.Join(dir, "config.json"))
	os.Remove(filepath.Join(dir, "artex-harness.json"))
	if got := Path(); got != filepath.Join(dir, "artex-harness.json") {
		t.Fatalf("default path: got %q", got)
	}
}

func TestPostgresDSNPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"database":{"host":"10.1.2.3","port":6000,"user":"u","password":"p","dbname":"d","sslmode":"require"}}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)

	// no env DSN → built from config file fields
	t.Setenv("ARTEX_PG_DSN", "")
	got, _, err := PostgresDSN()
	want := "postgres://u:p@10.1.2.3:6000/d?sslmode=require"
	if err != nil || got != want {
		t.Fatalf("from file: got %q err %v want %q", got, err, want)
	}

	// env wins over config file
	t.Setenv("ARTEX_PG_DSN", "postgres://envwins/x")
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://envwins/x" {
		t.Fatalf("env should win, got %q err %v", got, err)
	}

	// no env, no file → error (no built-in default)
	t.Setenv("ARTEX_PG_DSN", "")
	t.Setenv("ARTEX_CONFIG", filepath.Join(dir, "nope.json"))
	if got, _, err := PostgresDSN(); err == nil {
		t.Fatalf("missing config should error, got %q", got)
	}

	// full dsn in config file is used verbatim
	os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://full/dsn"}}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://full/dsn" {
		t.Fatalf("file dsn verbatim, got %q err %v", got, err)
	}
}
