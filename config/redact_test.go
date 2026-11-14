package config

import (
	"strings"
	"testing"
)

// TestRedactCoversBothDSNForms is the regression test for
// docs/upstream/SOURCE-REVIEW.md #4: a keyword/value DSN used to be returned
// verbatim (url.Parse sees it as an opaque path), so a failed connection printed
// the plaintext password into the server log and the UI log page.
func TestRedactCoversBothDSNForms(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings that must NOT appear
		keep []string // substrings that must survive (context preserved)
	}{
		{
			name: "url form",
			in:   "postgres://artex:s3cr3t@127.0.0.1:5432/artex?sslmode=disable",
			want: []string{"s3cr3t"},
			keep: []string{"postgres://", "artex", "127.0.0.1:5432", "sslmode=disable"},
		},
		{
			name: "keyword form",
			in:   "host=127.0.0.1 port=5432 user=artex password=s3cr3t dbname=artex",
			want: []string{"s3cr3t"},
			keep: []string{"host=127.0.0.1", "user=artex", "dbname=artex"},
		},
		{
			name: "keyword form with spaces around equals",
			in:   "host=127.0.0.1 password = s3cr3t dbname=artex",
			want: []string{"s3cr3t"},
			keep: []string{"host=127.0.0.1"},
		},
		{
			name: "keyword form quoted value with spaces",
			in:   `host=127.0.0.1 password='s3c r3t' dbname=artex`,
			want: []string{"s3c r3t", "s3cr3t"},
			keep: []string{"host=127.0.0.1", "dbname=artex"},
		},
		{
			name: "passfile and sslpassword",
			in:   "host=h passfile=/home/u/.pgpass sslpassword=topsecret dbname=d",
			want: []string{"/home/u/.pgpass", "topsecret"},
			keep: []string{"host=h", "dbname=d"},
		},
		{
			name: "no credentials",
			in:   "host=127.0.0.1 dbname=artex",
			want: nil,
			keep: []string{"host=127.0.0.1", "dbname=artex"},
		},
		{
			name: "empty",
			in:   "",
			want: nil,
			keep: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in)
			for _, w := range c.want {
				if strings.Contains(got, w) {
					t.Fatalf("secret %q leaked: %q", w, got)
				}
			}
			for _, k := range c.keep {
				if !strings.Contains(got, k) {
					t.Fatalf("context %q lost: %q", k, got)
				}
			}
		})
	}
}

// TestRedactKeywordFormStillUsableAsFingerprint: masking must not destroy the
// value entirely for diagnostics — the key name must remain visible.
func TestRedactKeepsKeyNames(t *testing.T) {
	got := Redact("host=h password=abc")
	if !strings.Contains(got, "password=") {
		t.Fatalf("key name removed, reducing diagnostic value: %q", got)
	}
	if strings.Contains(got, "abc") {
		t.Fatalf("value leaked: %q", got)
	}
}
