// Package config loads runtime configuration from a JSON file, with environment
// variables taking precedence. Currently it carries the PostgreSQL connection.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Database is the PostgreSQL connection config. Either set DSN directly, or set
// the component fields and a DSN is assembled from them.
type Database struct {
	DSN      string `json:"dsn"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

// Config is the on-disk config file shape.
type Config struct {
	Database Database `json:"database"`
	SkillDir string   `json:"skill_dir"`
}

// BaseDir is the directory that anchors all runtime artifacts (artex-harness.json and
// the data-harness/ store). It is the directory the running binary lives in, so a
// distributed executable keeps its files next to itself on any OS (Windows,
// Linux, …) regardless of the working directory it is launched from.
//
// When launched via `go run`, the binary is throwaway: it sits either in a temp
// build dir (cache miss → fresh link) OR straight inside the Go build cache
// (cache hit → run from $GOCACHE/.../...-d). We detect both and fall back to the
// current working directory so dev artifacts (data/, transcripts) and config
// resolve against the project dir, not the throwaway binary's location.
func BaseDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if isGoRunDir(dir) {
		return "." // throwaway `go run` binary → use CWD
	}
	return dir
}

// isGoRunDir reports whether dir is where `go run` parked its executable: under
// the system temp dir (cache miss), or anywhere inside a Go build cache
// (…/go-build/…, the cache-hit case — NOT under os.TempDir(), which is why the
// old temp-only check failed intermittently). In both cases the binary is
// throwaway, so config/data must resolve against the CWD.
func isGoRunDir(dir string) bool {
	if tmp := os.TempDir(); tmp != "" {
		if rel, err := filepath.Rel(tmp, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		if seg == "go-build" {
			return true
		}
	}
	return false
}

// ConfigFileName is the harness config file. It is deliberately different
// from upstream ARTEX's config.json: unzipping both products into the same
// folder must not make them share (and overwrite) one database config.
const ConfigFileName = "artex-harness.json"

// Path returns the config file path. Resolution order:
//  1. env HARNESS_CONFIG (explicit override)
//  2. ./artex-harness.json in the current working directory
//  3. artex-harness.json next to the executable
//  4. ./config.json / BaseDir()/config.json (legacy fallback: an install
//     created before the rename keeps working, but a fresh install
//     must use artex-harness.json)
//
// The upstream ARTEX_CONFIG variable is deliberately NOT read here: it points
// at ARTEX's own config.json (database `artex`), and honoring it would let a
// machine-global ARTEX_CONFIG silently redirect this binary at ARTEX's
// database — the same cross-talk the HARNESS_PG_DSN fix (3.24) closes for the
// DSN. The file fallbacks above already cover pre-rename installs, whose
// config.json lives in their own directory.
//
// The first existing file wins. If none exist, the CWD artex-harness.json
// path is returned so the "not found" message points at the file to create.
func Path() string {
	if v := strings.TrimSpace(os.Getenv("HARNESS_CONFIG")); v != "" {
		return v
	}
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, ConfigFileName))
	}
	candidates = append(candidates, filepath.Join(BaseDir(), ConfigFileName))
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "config.json"))
	}
	candidates = append(candidates, filepath.Join(BaseDir(), "config.json"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// Load reads and parses the config file. A missing/unreadable file yields a zero
// Config (so callers fall back to defaults) rather than an error.
func Load() Config {
	var c Config
	b, err := os.ReadFile(Path())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	return c
}

// SkillDir returns the skill root directory with precedence:
//
//	env ARTEX_SKILL_DIR  >  config file (skill_dir)  >  BaseDir()/skills
//
// The directory is created if it does not exist.
func SkillDir() string {
	var d string
	if v := strings.TrimSpace(os.Getenv("ARTEX_SKILL_DIR")); v != "" {
		d = v
	} else if v := strings.TrimSpace(Load().SkillDir); v != "" {
		d = v
	} else {
		d = filepath.Join(BaseDir(), "skills")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

// PostgresDSN resolves the connection string with precedence:
//
//	env HARNESS_PG_DSN  >  config file (database.dsn, or assembled from fields)
//
// The upstream ARTEX_PG_DSN variable is deliberately NEVER read here. It is a
// machine-global name shared with ARTEX itself: when both products run on one
// machine with ARTEX_PG_DSN set (e.g. from ARTEX's systemd unit or a shared
// shell profile), honoring it made this binary connect to ARTEX's `artex`
// database despite its own artex-harness.json saying `artex_harness` —
// silently merging the two products' data (2026-11-21, PROVENANCE 3.24).
// Operators who still have ARTEX_PG_DSN set get a startup warning from the
// server (server.NewManager); the variable is ignored, never consulted.
//
// There is NO built-in fallback: when neither source supplies a database config,
// it returns an error naming the config path it inspected, so startup fails loudly
// instead of silently connecting to a wrong default. source describes where the
// DSN came from (for startup logging).
func PostgresDSN() (dsn, source string, err error) {
	if v := strings.TrimSpace(os.Getenv("HARNESS_PG_DSN")); v != "" {
		return v, "环境变量 HARNESS_PG_DSN", nil
	}
	db := Load().Database
	if d := strings.TrimSpace(db.DSN); d != "" {
		return d, "配置文件 " + Path() + " (database.dsn)", nil
	}
	if db.Host != "" || db.DBName != "" || db.User != "" {
		return db.buildDSN(), "配置文件 " + Path() + " (database 字段)", nil
	}
	msg := fmt.Sprintf("未找到数据库配置：环境变量 HARNESS_PG_DSN 未设置，且配置文件 %s 未提供 database（dsn 或 host/user/dbname）。请创建该配置文件或设置环境变量后重试", Path())
	if v := strings.TrimSpace(os.Getenv("ARTEX_PG_DSN")); v != "" {
		msg += "（注：检测到已废弃的 ARTEX_PG_DSN，本程序不再读取它——如需用环境变量指定数据库，请改用 HARNESS_PG_DSN）"
	}
	return "", "", errors.New(msg)
}

func (d Database) buildDSN() string {
	host := d.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := d.Port
	if port == 0 {
		port = 5432
	}
	ssl := d.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + d.DBName,
	}
	if d.User != "" {
		if d.Password != "" {
			u.User = url.UserPassword(d.User, d.Password)
		} else {
			u.User = url.User(d.User)
		}
	}
	u.RawQuery = url.Values{"sslmode": {ssl}}.Encode()
	return u.String()
}

// String is a redacted view of the resolved DSN (password masked) for logging.
// redactedKeyValue matches libpq keyword/value URL options that carry a secret,
// in either `key=value` or `key = 'value'` form.
var redactedKeyValue = regexp.MustCompile(`(?i)\b(password|passfile|sslpassword)\s*=\s*(?:'[^']*'|"[^"]*"|[^\s]+)`)

// Redact removes credentials from a PostgreSQL DSN before it reaches a log line,
// an error message or the UI.
//
// Two forms must be handled:
//   - URL form  postgres://user:pass@host/db     → user info password masked;
//   - keyword/value form  host=... user=... password=...  (pgx/libpq native)
//
// The keyword form is NOT a URL: url.Parse treats it as an opaque path with an
// empty u.User, so the original implementation returned it verbatim and a failed
// connection printed the plaintext password into the server log and the UI log
// page — i.e. exactly when a user is most likely to paste logs for help.
// (上游复核 docs/upstream/SOURCE-REVIEW.md 第 4 条，本项目已修。)
func Redact(dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if u.User != nil {
			if _, hasPw := u.User.Password(); hasPw {
				u.User = url.UserPassword(u.User.Username(), "****")
			}
		}
		return u.String()
	}
	// Keyword/value form: mask every secret-bearing key.
	return redactedKeyValue.ReplaceAllString(dsn, "$1=****")
}
