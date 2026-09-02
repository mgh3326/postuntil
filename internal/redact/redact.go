package redact

import "strings"

// Headers copies headers with sensitive values removed, including Authorization.
func Headers(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if strings.EqualFold(k, "Authorization") || strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}
