package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mgh3326/postuntil/internal/predicate"
)

type Result struct {
	Outcome   string
	Polls     int
	LastState any
	Err       error
}

func Until(ctx context.Context, client *http.Client, url string, until, fail predicate.Predicate, hasFail bool, interval time.Duration, max int) Result {
	bad := 0
	r := Result{}
	for {
		if max > 0 && r.Polls >= max {
			r.Outcome = "timeout"
			return r
		}
		if err := ctx.Err(); err != nil {
			r.Outcome = "timeout"
			return r
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			r.Outcome = "error"
			r.Err = err
			return r
		}
		resp, err := client.Do(req)
		r.Polls++
		if err != nil {
			bad++
			if bad >= 4 {
				r.Outcome = "error"
				r.Err = err
				return r
			}
		} else {
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				r.Outcome = "error"
				r.Err = fmt.Errorf("poll returned HTTP %d", resp.StatusCode)
				return r
			}
			if resp.StatusCode >= 500 || readErr != nil {
				bad++
				if bad >= 4 {
					r.Outcome = "error"
					r.Err = fmt.Errorf("poll failed after 4 consecutive errors")
					return r
				}
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				bad = 0
				var doc any
				if e := json.Unmarshal(b, &doc); e != nil {
					r.Outcome = "error"
					r.Err = fmt.Errorf("poll response is not JSON: %w", e)
					return r
				}
				if v, ok := until.Value(doc); ok {
					r.LastState = v
				}
				if until.Match(doc) {
					r.Outcome = "success"
					return r
				}
				if hasFail && fail.Match(doc) {
					r.Outcome = "failure"
					return r
				}
			} else {
				bad++
				if bad >= 4 {
					r.Outcome = "error"
					r.Err = fmt.Errorf("poll returned HTTP %d", resp.StatusCode)
					return r
				}
			}
		}
		if max > 0 && r.Polls >= max {
			r.Outcome = "timeout"
			return r
		}
		select {
		case <-ctx.Done():
			r.Outcome = "timeout"
			return r
		case <-time.After(interval):
		}
	}
}
