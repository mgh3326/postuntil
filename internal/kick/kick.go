package kick

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mgh3326/postuntil/internal/predicate"
)

func CanonicalBody(body []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func AutoKey(url string, body []byte, now time.Time) string {
	h := sha256.Sum256([]byte(url + string(body) + now.UTC().Format("2006-01-02")))
	return hex.EncodeToString(h[:])[:32]
}
func Post(ctx context.Context, client *http.Client, url string, body []byte, headers map[string]string, idHeader, key string, idPath predicate.Predicate) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set(idHeader, key)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("POST returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var doc any
	if err = json.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("POST response is not JSON: %w", err)
	}
	v, ok := idPath.Value(doc)
	if !ok || fmt.Sprint(v) == "" {
		return "", fmt.Errorf("id path not found in POST response")
	}
	return fmt.Sprint(v), nil
}
func HeaderLines(headers map[string]string) []string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+headers[k])
	}
	return out
}
func ReplaceID(tmpl, id string) string { return strings.ReplaceAll(tmpl, "{{.id}}", id) }
