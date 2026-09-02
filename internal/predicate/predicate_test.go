package predicate

import "testing"

func TestNestedPathAndLiterals(t *testing.T) {
	doc := map[string]any{"items": []any{map[string]any{"state": "done", "count": float64(3), "ready": true}}}
	for _, text := range []string{".items[0].state=done", ".items[0].count=3", ".items[0].ready=true"} {
		p, err := Parse(text)
		if err != nil || !p.Match(doc) {
			t.Fatalf("%q did not match: %v", text, err)
		}
	}
}

func TestRejectsUnsupportedPath(t *testing.T) {
	if _, err := Parse("$.state=done"); err == nil {
		t.Fatal("accepted unsupported path syntax")
	}
}
