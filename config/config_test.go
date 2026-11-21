package config

import (
	"os"
	"path/filepath"
	"strings"
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
	// 上游 ARTEX_CONFIG 即使指向一个存在的文件也不再被读取：它指向上游
	// ARTEX 自己的 config.json（库 artex），读它就是混库（3.24）。
	t.Setenv("ARTEX_CONFIG", filepath.Join(dir, "config.json"))
	if got := Path(); got != filepath.Join(dir, "artex-harness.json") {
		t.Fatalf("ARTEX_CONFIG must be ignored: got %q", got)
	}
	t.Setenv("ARTEX_CONFIG", "")
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
	t.Setenv("HARNESS_CONFIG", cfgPath)
	t.Setenv("ARTEX_CONFIG", "")
	t.Setenv("ARTEX_PG_DSN", "")
	t.Setenv("HARNESS_PG_DSN", "")

	// no env DSN → built from config file fields
	got, _, err := PostgresDSN()
	want := "postgres://u:p@10.1.2.3:6000/d?sslmode=require"
	if err != nil || got != want {
		t.Fatalf("from file: got %q err %v want %q", got, err, want)
	}

	// HARNESS_PG_DSN wins over config file
	t.Setenv("HARNESS_PG_DSN", "postgres://envwins/x")
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://envwins/x" {
		t.Fatalf("env should win, got %q err %v", got, err)
	}
	t.Setenv("HARNESS_PG_DSN", "")

	// 上游 ARTEX_PG_DSN 即使已设置也被忽略：配置文件获胜（3.24 混库根因）。
	t.Setenv("ARTEX_PG_DSN", "postgres://artexdb/artex")
	if got, _, err := PostgresDSN(); err != nil || got != want {
		t.Fatalf("ARTEX_PG_DSN must be ignored, got %q err %v want %q", got, err, want)
	}

	// 只有 ARTEX_PG_DSN、没有配置文件 → 报错而不是连向 ARTEX 的库，
	// 且错误里要同时点出两个变量名，免得运维继续往旧变量里填。
	t.Setenv("HARNESS_CONFIG", filepath.Join(dir, "nope.json"))
	if _, _, err := PostgresDSN(); err == nil {
		t.Fatalf("ARTEX_PG_DSN alone should error")
	} else if !strings.Contains(err.Error(), "HARNESS_PG_DSN") || !strings.Contains(err.Error(), "ARTEX_PG_DSN") {
		t.Fatalf("error should name both vars, got %v", err)
	}
	t.Setenv("ARTEX_PG_DSN", "")

	// no env, no file → error (no built-in default)
	t.Setenv("HARNESS_PG_DSN", "")
	if got, _, err := PostgresDSN(); err == nil {
		t.Fatalf("missing config should error, got %q", got)
	}

	// full dsn in config file is used verbatim
	os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://full/dsn"}}`), 0o644)
	t.Setenv("HARNESS_CONFIG", cfgPath)
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://full/dsn" {
		t.Fatalf("file dsn verbatim, got %q err %v", got, err)
	}
}
