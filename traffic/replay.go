// Structured traffic table + replay (stage 2, P1).
//
// The recorder already has Traffic.Page (SQL-filtered metadata) and
// Traffic.Get (full request/response text), but nothing turns a recorded
// exchange into a replayable request, and nothing presents the capture as
// an F12-style table in one place. This file is that layer, in pure
// functions plus one small HTTP executor:
//
//   - Row: the F12 row for one ExchangeMeta (status class, path, size).
//   - Filter: in-memory filtering for the table (host / method / status
//     class / text), mirroring Page's SQL filters so the UI and an export
//     agree on what "filtered" means.
//   - ReplaySpec: a parsed, secret-redacted replay request built from the
//     recorded raw request text. Authorization / Cookie / proxy-auth
//     headers are replaced with "[redacted]" in the spec and in the
//     generated curl command, so a replay description can be pasted into
//     a report without leaking credentials.
//   - Replay: executes a ReplaySpec with a timeout and reports whether
//     the response reproduced the original (same status class, and same
//     status code when the original was not an error).
package traffic

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Row is one F12-style table row.
type Row struct {
	ID          string `json:"id"`
	TS          int64  `json:"ts"`
	Host        string `json:"host"`
	Method      string `json:"method"`
	URL         string `json:"url"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	StatusClass string `json:"status_class"`
	ContentType string `json:"content_type"`
	RespLen     int    `json:"resp_len"`
}

// StatusClassOf maps a status code to its F12 class bucket.
func StatusClassOf(status int) string {
	switch {
	case status >= 100 && status < 200:
		return "1xx"
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "other"
	}
}

// RowsFromMeta converts recorded exchange metadata into F12 rows.
// Path is taken from the URL (query stripped); a malformed URL keeps the
// raw URL as its path so the row is still shown.
func RowsFromMeta(metas []ExchangeMeta) []Row {
	rows := make([]Row, 0, len(metas))
	for _, m := range metas {
		path := m.URL
		if u, err := url.Parse(m.URL); err == nil && u.Path != "" {
			path = u.Path
		}
		rows = append(rows, Row{
			ID: m.ID, TS: m.TS, Host: m.Host, Method: m.Method, URL: m.URL,
			Path: path, Status: m.Status, StatusClass: StatusClassOf(m.Status),
			ContentType: m.ContentType, RespLen: m.RespLen,
		})
	}
	return rows
}

// RowFilter is the F12 table filter. All set fields are ANDed.
type RowFilter struct {
	Host        string // host substring (case-insensitive)
	Method      string // exact method (case-insensitive)
	StatusClass string // "2xx" / "4xx" / ...
	Status      int    // exact status code, 0 = any
	Query       string // substring over URL / path / content type
	MinRespLen  int    // -1 / 0 = no lower bound
}

// FilterRows applies f to rows, preserving order.
func FilterRows(rows []Row, f RowFilter) []Row {
	var out []Row
	host := strings.ToLower(strings.TrimSpace(f.Host))
	method := strings.ToUpper(strings.TrimSpace(f.Method))
	class := strings.ToLower(strings.TrimSpace(f.StatusClass))
	query := strings.ToLower(strings.TrimSpace(f.Query))
	for _, r := range rows {
		if host != "" && !strings.Contains(strings.ToLower(r.Host), host) {
			continue
		}
		if method != "" && strings.ToUpper(r.Method) != method {
			continue
		}
		if class != "" && r.StatusClass != class {
			continue
		}
		if f.Status != 0 && r.Status != f.Status {
			continue
		}
		if f.MinRespLen > 0 && r.RespLen < f.MinRespLen {
			continue
		}
		if query != "" {
			hay := strings.ToLower(r.URL + " " + r.Path + " " + r.ContentType + " " + r.Host)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// ReplaySpec is a redacted, replayable description of a recorded request.
type ReplaySpec struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body,omitempty"`
	OriginalStatus int               `json:"original_status"`
	ExchangeID     string            `json:"exchange_id"`
	// Redacted lists header names whose values were replaced.
	Redacted []string `json:"redacted,omitempty"`
}

// redactedHeaders are never carried into a replay spec verbatim.
var redactedHeaders = map[string]bool{
	"authorization": true, "cookie": true, "proxy-authorization": true,
	"x-api-key": true, "api-key": true,
}

// ParseReplaySpec parses raw request text (as returned by Traffic.Get:
// request line, headers, blank line, body) into a ReplaySpec. The URL is
// rebuilt from the Host header + request target when the target is a path.
func ParseReplaySpec(exchangeID, rawRequest string, originalStatus int) (ReplaySpec, error) {
	spec := ReplaySpec{ExchangeID: exchangeID, OriginalStatus: originalStatus, Headers: map[string]string{}}
	sc := bufio.NewScanner(strings.NewReader(rawRequest))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	first := true
	inBody := false
	var body strings.Builder
	var host string
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return spec, fmt.Errorf("请求行无法解析：%q", line)
			}
			spec.Method = strings.ToUpper(parts[0])
			spec.URL = parts[1] // path or absolute URL; fixed below
			continue
		}
		if inBody {
			body.WriteString(line)
			body.WriteString("\n")
			continue
		}
		if strings.TrimSpace(line) == "" {
			inBody = true
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		lk := strings.ToLower(k)
		if redactedHeaders[lk] {
			spec.Redacted = append(spec.Redacted, k)
			v = "[redacted]"
		}
		if lk == "host" {
			host = v
		}
		spec.Headers[k] = v
	}
	if err := sc.Err(); err != nil {
		return spec, err
	}
	if spec.Method == "" {
		return spec, fmt.Errorf("请求为空")
	}
	spec.Body = strings.TrimRight(body.String(), "\n")
	if !strings.HasPrefix(spec.URL, "http://") && !strings.HasPrefix(spec.URL, "https://") {
		if host == "" {
			return spec, fmt.Errorf("请求缺少 Host，无法重建 URL")
		}
		// Recorded traffic is HTTPS through the MITM proxy unless the
		// target was plain HTTP; the request line alone cannot tell, so
		// default to https and let the caller override via URL if needed.
		spec.URL = "https://" + host + spec.URL
	}
	return spec, nil
}

// CurlCommand renders the spec as a pasteable curl command. Bodies are
// capped at 4 KiB in the command so a large replay does not flood a report.
func (s ReplaySpec) CurlCommand() string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -X %s %s", s.Method, strconv.Quote(s.URL))
	for k, v := range s.Headers {
		if strings.EqualFold(k, "host") {
			continue
		}
		fmt.Fprintf(&b, " -H %s", strconv.Quote(k+": "+v))
	}
	if s.Body != "" {
		body := s.Body
		if len(body) > 4096 {
			body = body[:4096] + "…[truncated]"
		}
		fmt.Fprintf(&b, " --data %s", strconv.Quote(body))
	}
	return b.String()
}

// ReplayResult is the outcome of executing a ReplaySpec.
type ReplayResult struct {
	Status     int    `json:"status"`
	RespLen    int    `json:"resp_len"`
	Reproduced bool   `json:"reproduced"`
	Note       string `json:"note"`
}

// Reproduced means the replay got the same status class as the original,
// and the same code when the original was a success (2xx). An error
// status is class-matched only: a 500 today vs a 500 last week is the
// same class of failure, not proof the same bug fired.
func Reproduced(original, got int) bool {
	if StatusClassOf(original) != StatusClassOf(got) {
		return false
	}
	if StatusClassOf(original) == "2xx" {
		return original == got
	}
	return true
}

// Replay executes spec against the live target with a 30s timeout and
// reports whether the response reproduced the original exchange.
func Replay(ctx context.Context, spec ReplaySpec) (ReplayResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body io.Reader
	if spec.Body != "" {
		body = strings.NewReader(spec.Body)
	}
	req, err := http.NewRequestWithContext(ctx, spec.Method, spec.URL, body)
	if err != nil {
		return ReplayResult{}, err
	}
	for k, v := range spec.Headers {
		if v == "[redacted]" {
			continue // redacted credentials are never sent on replay
		}
		if strings.EqualFold(k, "host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ReplayResult{Reproduced: false, Note: "重放请求失败：" + err.Error()}, nil
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	return ReplayResult{
		Status: resp.StatusCode, RespLen: int(n),
		Reproduced: Reproduced(spec.OriginalStatus, resp.StatusCode),
		Note:       fmt.Sprintf("原始状态 %d，重放状态 %d", spec.OriginalStatus, resp.StatusCode),
	}, nil
}
