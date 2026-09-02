package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgh3326/postuntil/internal/kick"
)

func runForTest(t *testing.T, args []string, env ...string) (int, string, string) {
	t.Helper()
	var out, err bytes.Buffer
	code := Run(args, &out, &err, env)
	return code, out.String(), err.String()
}
func argsFor(server string) []string {
	return []string{"run", "--post", server + "/post", "--body", `{"x":1}`, "--id-path", ".task_id", "--poll", server + "/poll/{{.id}}", "--until", ".state=done", "--interval", "1ms", "--timeout", "200ms"}
}

func TestHappyFailTimeoutAndMaxPolls(t *testing.T) {
	tests := []struct {
		name    string
		states  []string
		extra   []string
		want    int
		outcome string
	}{
		{"happy", []string{"working", "done"}, nil, ExitSuccess, "success"},
		{"fail", []string{"error"}, []string{"--fail-when", ".state=error"}, ExitFailure, "failure"},
		{"timeout", []string{"working"}, []string{"--timeout", "5ms"}, ExitTimeout, "timeout"},
		{"max polls", []string{"working"}, []string{"--max-polls", "1"}, ExitTimeout, "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			i := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/post" {
					fmt.Fprint(w, `{"task_id":"a1"}`)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				state := tt.states[min(i, len(tt.states)-1)]
				i++
				fmt.Fprintf(w, `{"state":%q}`, state)
			}))
			defer s.Close()
			a := append(argsFor(s.URL), tt.extra...)
			code, out, _ := runForTest(t, a)
			if code != tt.want || !strings.Contains(out, `"outcome":"`+tt.outcome+`"`) {
				t.Fatalf("code=%d output=%s", code, out)
			}
		})
	}
}
func TestPoll4xxAndFour5xxAreRequestErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			n := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/post" {
					fmt.Fprint(w, `{"task_id":"a"}`)
					return
				}
				n++
				http.Error(w, "no", status)
			}))
			defer s.Close()
			code, out, _ := runForTest(t, argsFor(s.URL))
			wantPolls := 1
			if status >= 500 {
				wantPolls = 4
			}
			if code != ExitRequest || !strings.Contains(out, fmt.Sprintf(`"polls":%d`, wantPolls)) {
				t.Fatalf("code %d output %s", code, out)
			}
		})
	}
}
func TestMissingIDIsRequestError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"other":"a"}`) }))
	defer s.Close()
	code, _, _ := runForTest(t, argsFor(s.URL))
	if code != ExitRequest {
		t.Fatal(code)
	}
}
func TestPostIsNotRepeatedOnRedirect(t *testing.T) {
	posts := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/post") {
			posts++
			http.Redirect(w, r, "/post-again", http.StatusTemporaryRedirect)
		}
	}))
	defer s.Close()
	code, _, _ := runForTest(t, argsFor(s.URL))
	if code != ExitRequest || posts != 1 {
		t.Fatalf("code=%d posts=%d", code, posts)
	}
}
func TestAutoKeyStableAndBodySpecific(t *testing.T) {
	now := time.Date(2026, 9, 2, 20, 0, 0, 0, time.FixedZone("x", 3*3600))
	a := kick.AutoKey("https://example.internal/x", []byte(`{"a":1}`), now)
	if a != kick.AutoKey("https://example.internal/x", []byte(`{"a":1}`), now) || a == kick.AutoKey("https://example.internal/x", []byte(`{"a":2}`), now) {
		t.Fatal("auto key is not stable/body-specific")
	}
}
func TestIdempotencyHeaderAndHeaderEnvRedaction(t *testing.T) {
	secret := "never-print-this-value"
	seen := ""
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Idempotency-Key")
		fmt.Fprint(w, `{"task_id":"x"}`)
	}))
	defer s.Close()
	a := argsFor(s.URL)
	a = append(a, "--header-env", "X-Api-Key=JOB_TOKEN", "--dry-run")
	code, out, err := runForTest(t, a, "JOB_TOKEN="+secret)
	if code != ExitSuccess || seen != "" || strings.Contains(out+err, secret) || !strings.Contains(err, "X-Api-Key=***") {
		t.Fatalf("code=%d seen=%q output=%q err=%q", code, seen, out, err)
	}
	// Authorization is redacted even when it was supplied directly.
	a = append(argsFor(s.URL), "--header", "Authorization="+secret, "--dry-run")
	code, out, err = runForTest(t, a)
	if code != ExitSuccess || strings.Contains(out+err, secret) || !strings.Contains(err, "Authorization=***") {
		t.Fatalf("authorization redaction failed: %q", err)
	}
	// A real request also carries the generated idempotency header.
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/post" {
			seen = r.Header.Get("Idempotency-Key")
			fmt.Fprint(w, `{"task_id":"x"}`)
			return
		}
		fmt.Fprint(w, `{"state":"done"}`)
	}))
	defer s2.Close()
	code, _, err = runForTest(t, argsFor(s2.URL))
	if code != ExitSuccess || len(seen) != 32 || strings.Contains(err, secret) {
		t.Fatalf("code=%d key=%q", code, seen)
	}
}
func TestHealthPingIsAfterOutcomeAndFailureUsesFail(t *testing.T) {
	var events []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/post":
			fmt.Fprint(w, `{"task_id":"x"}`)
		case "/poll/x":
			events = append(events, "poll")
			fmt.Fprint(w, `{"state":"error"}`)
		case "/hc/fail":
			events = append(events, "fail")
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer s.Close()
	a := append(argsFor(s.URL), "--fail-when", ".state=error", "--hc-ping", s.URL+"/hc")
	code, _, _ := runForTest(t, a)
	if code != ExitFailure || strings.Join(events, ",") != "poll,fail" {
		t.Fatalf("code=%d events=%v", code, events)
	}
}
func TestTOMLFlagsWin(t *testing.T) {
	var got string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		if r.URL.Path == "/override" {
			fmt.Fprint(w, `{"task_id":"x"}`)
			return
		}
		fmt.Fprint(w, `{"state":"done"}`)
	}))
	defer s.Close()
	p := filepath.Join(t.TempDir(), "job.toml")
	content := fmt.Sprintf("post = %q\nbody = '{}'\nid_path = '.task_id'\npoll = %q\nuntil = '.state=done'\ninterval = '1ms'\ntimeout = '1s'\n", s.URL+"/wrong", s.URL+"/poll/{{.id}}")
	if e := os.WriteFile(p, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	code, _, _ := runForTest(t, []string{"run", "-f", p, "--post", s.URL + "/override"})
	if code != ExitSuccess || got != "/poll/x" {
		t.Fatalf("code=%d got=%s", code, got)
	}
}
func TestExitCodeTable(t *testing.T) {
	if ExitSuccess != 0 || ExitFailure != 1 || ExitTimeout != 2 || ExitRequest != 3 || ExitConfig != 4 {
		t.Fatal("exit code contract changed")
	}
	code, _, _ := runForTest(t, []string{"run"})
	if code != ExitConfig {
		t.Fatal(code)
	}
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
