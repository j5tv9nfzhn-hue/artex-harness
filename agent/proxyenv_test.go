package agent

import (
	"strings"
	"testing"
)

func TestProxyEnvEmptyIsNil(t *testing.T) {
	if env := proxyEnv("", ""); env != nil {
		t.Fatalf("proxyEnv(\"\", \"\") = %v, want nil (direct)", env)
	}
}

func TestProxyEnvSetsAllProxyForSocks5(t *testing.T) {
	// Capture-off egress path: a socks5 proxy, no MITM CA. ALL_PROXY must be set
	// (curl reads socks5 only from there), and no CA vars should appear.
	env := proxyEnv("socks5://10.0.0.1:1080", "")
	has := func(prefix string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "all_proxy="} {
		if !has(want) {
			t.Errorf("proxyEnv missing %s: %v", want, env)
		}
	}
	if has("SSL_CERT_FILE=") || has("CURL_CA_BUNDLE=") {
		t.Errorf("proxyEnv without CA must not inject CA vars: %v", env)
	}
}

func TestProxyEnvInjectsCAWhenRecording(t *testing.T) {
	env := proxyEnv("http://127.0.0.1:8788", "/data/ca.pem")
	has := func(prefix string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"SSL_CERT_FILE=", "CURL_CA_BUNDLE=", "REQUESTS_CA_BUNDLE=", "NODE_EXTRA_CA_CERTS="} {
		if !has(want) {
			t.Errorf("proxyEnv with CA missing %s: %v", want, env)
		}
	}
}

func TestProxyEnvClearsNoProxyWhenRecording(t *testing.T) {
	// Capture on (MITM CA present): NO_PROXY/no_proxy must be forced
	// empty, otherwise curl/Python honor the host's no_proxy and
	// loopback targets dial direct, leaving zero records (real-task #1:
	// 6,523 agent requests, 0 recorded).
	env := proxyEnv("http://127.0.0.1:8798", "/data/ca.pem")
	val := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			val[k] = v
		}
	}
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		v, ok := val[k]
		if !ok {
			t.Errorf("capture-on proxyEnv missing %s (must force empty): %v", k, env)
		} else if v != "" {
			t.Errorf("capture-on proxyEnv %s=%q, want empty", k, v)
		}
	}
}

func TestProxyEnvKeepsNoProxyWhenCaptureOff(t *testing.T) {
	// Capture off (no MITM CA): the user's NO_PROXY must be left alone —
	// only the recording path may override it.
	env := proxyEnv("socks5://10.0.0.1:1080", "")
	for _, e := range env {
		if strings.HasPrefix(e, "NO_PROXY=") || strings.HasPrefix(e, "no_proxy=") {
			t.Errorf("capture-off proxyEnv must not touch NO_PROXY, got %v", env)
		}
	}
}

func TestBashEnvScrubsSecrets(t *testing.T) {
	t.Setenv("SANDBOX_TEST_DEMO_TOKEN", "sk-should-not-leak")
	t.Setenv("SANDBOX_TEST_DEMO_API_KEY", "ak-should-not-leak")
	t.Setenv("SANDBOX_TEST_BENIGN", "keep-me")
	env := bashEnv("http://127.0.0.1:8798", "/data/ca.pem")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "sk-should-not-leak") || strings.Contains(joined, "ak-should-not-leak") {
		t.Errorf("bashEnv leaked secret values: %v", env)
	}
	for _, want := range []string{"SANDBOX_TEST_DEMO_TOKEN=", "SANDBOX_TEST_DEMO_API_KEY="} {
		found := false
		for _, e := range env {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("bashEnv missing scrub override %q: %v", want, env)
		}
	}
	if strings.Contains(joined, "SANDBOX_TEST_BENIGN=") {
		t.Errorf("bashEnv must not override benign vars: %v", env)
	}
	if !strings.Contains(joined, "HTTP_PROXY=http://127.0.0.1:8798") {
		t.Errorf("bashEnv lost proxy routing: %v", env)
	}
	// No proxy configured: scrubbing still applies, no proxy vars appear.
	env2 := bashEnv("", "")
	joined2 := strings.Join(env2, "\n")
	if !strings.Contains(joined2, "SANDBOX_TEST_DEMO_TOKEN=") {
		t.Errorf("bashEnv without proxy must still scrub: %v", env2)
	}
	if strings.Contains(joined2, "HTTP_PROXY=") {
		t.Errorf("bashEnv without proxy must not add proxy vars: %v", env2)
	}
}
