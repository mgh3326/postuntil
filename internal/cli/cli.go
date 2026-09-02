// Package cli owns command parsing and the command's stable exit-code contract.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mgh3326/postuntil/internal/hc"
	"github.com/mgh3326/postuntil/internal/kick"
	"github.com/mgh3326/postuntil/internal/poll"
	"github.com/mgh3326/postuntil/internal/predicate"
	"github.com/mgh3326/postuntil/internal/redact"
)

const Version = "v0.0.0"
const (
	ExitSuccess = 0
	ExitFailure = 1
	ExitTimeout = 2
	ExitRequest = 3
	ExitConfig  = 4
)

type Config struct {
	Post              string            `toml:"post"`
	Body              string            `toml:"body"`
	Headers           map[string]string `toml:"headers"`
	HeaderEnv         map[string]string `toml:"header_env"`
	IdempotencyKey    string            `toml:"idempotency_key"`
	IdempotencyHeader string            `toml:"idempotency_header"`
	IDPath            string            `toml:"id_path"`
	Poll              string            `toml:"poll"`
	Until             string            `toml:"until"`
	FailWhen          string            `toml:"fail_when"`
	Interval          string            `toml:"interval"`
	Timeout           string            `toml:"timeout"`
	MaxPolls          int               `toml:"max_polls"`
	HCPing            string            `toml:"hc_ping"`
	DryRun            bool              `toml:"dry_run"`
	Quiet             bool              `toml:"quiet"`
	JSON              bool              `toml:"json"`
}
type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }
func Run(args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, Version)
		return ExitSuccess
	}
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(stderr, "usage: postuntil run [options] | postuntil version")
		return ExitConfig
	}
	return run(args[1:], stdout, stderr, environ, time.Now)
}
func run(args []string, stdout, stderr io.Writer, environ []string, now func() time.Time) int {
	var file string
	for i, a := range args {
		if (a == "-f" || a == "--file") && i+1 < len(args) {
			file = args[i+1]
		}
	}
	c := Config{Body: "{}", IdempotencyKey: "auto", IdempotencyHeader: "Idempotency-Key", Interval: "5s", Timeout: "30m"}
	if file != "" {
		if _, err := toml.DecodeFile(file, &c); err != nil {
			fmt.Fprintf(stderr, "configuration error: %v\n", err)
			return ExitConfig
		}
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var headers, headerEnv stringsFlag
	fs.StringVar(&file, "f", file, "TOML job file")
	fs.StringVar(&c.Post, "post", c.Post, "POST URL")
	fs.StringVar(&c.Body, "body", c.Body, "JSON body or @file")
	fs.Var(&headers, "header", "HTTP header K=V (repeatable)")
	fs.Var(&headerEnv, "header-env", "HTTP header K=environment variable (repeatable)")
	fs.StringVar(&c.IdempotencyKey, "idempotency-key", c.IdempotencyKey, "auto or value")
	fs.StringVar(&c.IdempotencyHeader, "idempotency-header", c.IdempotencyHeader, "header name")
	fs.StringVar(&c.IDPath, "id-path", c.IDPath, "response JSON path")
	fs.StringVar(&c.Poll, "poll", c.Poll, "poll URL template")
	fs.StringVar(&c.Until, "until", c.Until, "success predicate")
	fs.StringVar(&c.FailWhen, "fail-when", c.FailWhen, "failure predicate")
	fs.StringVar(&c.Interval, "interval", c.Interval, "poll interval")
	fs.StringVar(&c.Timeout, "timeout", c.Timeout, "overall timeout")
	fs.IntVar(&c.MaxPolls, "max-polls", c.MaxPolls, "maximum polls")
	fs.StringVar(&c.HCPing, "hc-ping", c.HCPing, "health ping URL")
	fs.BoolVar(&c.DryRun, "dry-run", c.DryRun, "print request without network")
	fs.BoolVar(&c.Quiet, "quiet", c.Quiet, "suppress progress logs")
	fs.BoolVar(&c.JSON, "json", c.JSON, "emit JSON summary")
	if err := fs.Parse(args); err != nil {
		return ExitConfig
	}
	if fs.NArg() != 0 {
		return configErr(stderr, "unexpected positional arguments")
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	if c.HeaderEnv == nil {
		c.HeaderEnv = map[string]string{}
	}
	for _, s := range headers {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return configErr(stderr, "--header must be K=V")
		}
		c.Headers[k] = v
	}
	for _, s := range headerEnv {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" || v == "" {
			return configErr(stderr, "--header-env must be K=ENVNAME")
		}
		c.HeaderEnv[k] = v
	}
	return execute(c, stdout, stderr, environ, now)
}
func configErr(w io.Writer, s string) int {
	fmt.Fprintf(w, "configuration error: %s\n", s)
	return ExitConfig
}
func execute(c Config, stdout, stderr io.Writer, environ []string, now func() time.Time) int {
	if c.Post == "" || c.IDPath == "" || c.Poll == "" || c.Until == "" {
		return configErr(stderr, "--post, --id-path, --poll, and --until are required")
	}
	if !strings.Contains(c.Poll, "{{.id}}") {
		return configErr(stderr, "--poll must include {{.id}}")
	}
	until, e := predicate.Parse(c.Until)
	if e != nil {
		return configErr(stderr, e.Error())
	}
	var fail predicate.Predicate
	hasFail := c.FailWhen != ""
	if hasFail {
		fail, e = predicate.Parse(c.FailWhen)
		if e != nil {
			return configErr(stderr, e.Error())
		}
	}
	interval, e := time.ParseDuration(c.Interval)
	if e != nil || interval <= 0 {
		return configErr(stderr, "invalid --interval")
	}
	timeout, e := time.ParseDuration(c.Timeout)
	if e != nil || timeout <= 0 {
		return configErr(stderr, "invalid --timeout")
	}
	if c.MaxPolls < 0 {
		return configErr(stderr, "--max-polls must not be negative")
	}
	body, e := bodyBytes(c.Body)
	if e != nil {
		return configErr(stderr, e.Error())
	}
	body, e = kick.CanonicalBody(body)
	if e != nil {
		return configErr(stderr, "body must be JSON")
	}
	env := envMap(environ)
	headers := map[string]string{}
	for k, v := range c.Headers {
		headers[k] = v
	}
	secretHeaders := map[string]bool{}
	for k, name := range c.HeaderEnv {
		v, ok := env[name]
		if !ok {
			return configErr(stderr, "environment variable for --header-env is not set")
		}
		headers[k] = v
		secretHeaders[k] = true
	}
	key := c.IdempotencyKey
	if key == "" || key == "auto" {
		key = kick.AutoKey(c.Post, body, now())
	}
	if c.IdempotencyHeader == "" {
		return configErr(stderr, "--idempotency-header must not be empty")
	}
	redacted := redact.Headers(headers)
	for k := range secretHeaders {
		redacted[k] = "***"
	}
	if c.DryRun {
		fmt.Fprintf(stderr, "dry-run POST %s headers=%s idempotency=%s body=%s\n", c.Post, strings.Join(kick.HeaderLines(redacted), ","), c.IdempotencyHeader, string(body))
		summary(stdout, "dry_run", "", 0, 0, nil)
		return ExitSuccess
	}
	started := now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// A redirect can otherwise cause an HTTP client to issue a second POST.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	id, e := kick.Post(ctx, client, c.Post, body, headers, c.IdempotencyHeader, key, mustPath(c.IDPath))
	if e != nil {
		fmt.Fprintf(stderr, "request error: %v\n", e)
		summary(stdout, "error", "", 0, time.Since(started), nil)
		return ExitRequest
	}
	if !c.Quiet {
		fmt.Fprintf(stderr, "POST accepted; polling id %s\n", id)
	}
	r := poll.Until(ctx, client, kick.ReplaceID(c.Poll, id), until, fail, hasFail, interval, c.MaxPolls)
	elapsed := time.Since(started)
	code := ExitRequest
	switch r.Outcome {
	case "success":
		code = ExitSuccess
	case "failure":
		code = ExitFailure
	case "timeout":
		code = ExitTimeout
	}
	if r.Err != nil {
		fmt.Fprintf(stderr, "request error: %v\n", r.Err)
	}
	summary(stdout, r.Outcome, id, r.Polls, elapsed, r.LastState)
	if c.HCPing != "" {
		if e := hc.Ping(context.Background(), client, c.HCPing, r.Outcome); e != nil {
			fmt.Fprintf(stderr, "health ping failed: %v\n", e)
		}
	}
	return code
}
func mustPath(s string) predicate.Predicate { p, _ := predicate.Parse(s + "=x"); return p }
func bodyBytes(s string) ([]byte, error) {
	if strings.HasPrefix(s, "@") {
		b, e := os.ReadFile(strings.TrimPrefix(s, "@"))
		if e != nil {
			return nil, fmt.Errorf("cannot read body file: %w", e)
		}
		return b, nil
	}
	return []byte(s), nil
}
func envMap(in []string) map[string]string {
	m := map[string]string{}
	for _, s := range in {
		k, v, ok := strings.Cut(s, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}
func summary(w io.Writer, outcome, id string, polls int, elapsed time.Duration, last any) {
	b, _ := json.Marshal(struct {
		Outcome string `json:"outcome"`
		ID      string `json:"id"`
		Polls   int    `json:"polls"`
		Elapsed int64  `json:"elapsed_ms"`
		Last    any    `json:"last_state"`
	}{outcome, id, polls, elapsed.Milliseconds(), last})
	fmt.Fprintln(w, string(b))
}
