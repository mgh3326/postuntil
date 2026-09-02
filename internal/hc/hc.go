package hc

import (
	"context"
	"net/http"
)

func Ping(ctx context.Context, client *http.Client, base, outcome string) error {
	url := base
	if outcome != "success" {
		url += "/fail"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{resp.StatusCode}
	}
	return nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "health ping returned non-success status" }
