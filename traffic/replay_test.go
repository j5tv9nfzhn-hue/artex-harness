package traffic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRowsAndFilter(t *testing.T) {
	metas := []ExchangeMeta{
		{ID: "1", Host: "a.example.com", Method: "GET", URL: "https://a.example.com/login?x=1", Status: 200, RespLen: 100},
		{ID: "2", Host: "a.example.com", Method: "POST", URL: "https://a.example.com/login", Status: 302, RespLen: 0},
		{ID: "3", Host: "b.example.com", Method: "GET", URL: "https://b.example.com/admin", Status: 403, RespLen: 50},
	}
	rows := RowsFromMeta(metas)
	if len(rows) != 3 || rows[0].Path != "/login" || rows[0].StatusClass != "2xx" || rows[2].StatusClass != "4xx" {
		t.Fatalf("rows wrong: %+v", rows)
	}
	if got := FilterRows(rows, RowFilter{Host: "a.example"}); len(got) != 2 {
		t.Fatalf("host filter: %d", len(got))
	}
	if got := FilterRows(rows, RowFilter{StatusClass: "4xx"}); len(got) != 1 || got[0].ID != "3" {
		t.Fatalf("status class filter: %+v", got)
	}
	if got := FilterRows(rows, RowFilter{Method: "post"}); len(got) != 1 {
		t.Fatalf("method filter (case-insensitive): %d", len(got))
	}
	if got := FilterRows(rows, RowFilter{Query: "admin"}); len(got) != 1 {
		t.Fatalf("query filter: %d", len(got))
	}
	if got := FilterRows(rows, RowFilter{Status: 302}); len(got) != 1 {
		t.Fatalf("exact status filter: %d", len(got))
	}
}

func TestParseReplaySpecRedacts(t *testing.T) {
	raw := "POST /login HTTP/1.1\nHost: a.example.com\nAuthorization: Bearer secret-token\nCookie: session=abc\nContent-Type: application/json\n\n{\"u\":\"a\"}"
	spec, err := ParseReplaySpec("ex-1", raw, 302)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Method != "POST" || spec.URL != "https://a.example.com/login" {
		t.Fatalf("spec: %+v", spec)
	}
	if spec.Headers["Authorization"] != "[redacted]" || spec.Headers["Cookie"] != "[redacted]" {
		t.Fatalf("credentials must be redacted: %v", spec.Headers)
	}
	if spec.Body != `{"u":"a"}` {
		t.Fatalf("body: %q", spec.Body)
	}
	curl := spec.CurlCommand()
	if strings.Contains(curl, "secret-token") || strings.Contains(curl, "session=abc") {
		t.Fatalf("curl must not leak credentials: %s", curl)
	}
	if !strings.Contains(curl, "[redacted]") {
		t.Fatalf("curl should show redaction: %s", curl)
	}
	if _, err := ParseReplaySpec("x", "garbage", 200); err == nil {
		t.Fatalf("unparseable request line must fail")
	}
}

func TestReplayReproduced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	spec := ReplaySpec{Method: "GET", URL: srv.URL + "/login", Headers: map[string]string{}, OriginalStatus: 200}
	res, err := Replay(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reproduced || res.Status != 200 {
		t.Fatalf("same 200 should reproduce: %+v", res)
	}
	// Original 201 vs replay 200: same class, different code — not reproduced.
	spec.OriginalStatus = 201
	res, _ = Replay(context.Background(), spec)
	if res.Reproduced {
		t.Fatalf("201 vs 200 must not count as reproduced: %+v", res)
	}
	// Original 500 vs replay 500: class match counts.
	if !Reproduced(500, 503) {
		t.Fatalf("5xx class match should reproduce")
	}
	if Reproduced(200, 404) {
		t.Fatalf("2xx vs 4xx must not reproduce")
	}
}
