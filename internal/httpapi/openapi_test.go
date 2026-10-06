package httpapi

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every /api route registered in Register must be documented in openapi.json,
// with the same method, so the spec used in Swagger/Postman cannot drift.
func TestOpenAPICoversRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	src, err := os.ReadFile("httpapi.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PUT|PATCH|DELETE) (/api/[^"]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(routes) == 0 {
		t.Fatal("no routes found in httpapi.go")
	}
	for _, r := range routes {
		method, path := strings.ToLower(r[1]), r[2]
		ops, ok := spec.Paths[path]
		if !ok {
			t.Errorf("%s %s is not documented in openapi.json", r[1], path)
			continue
		}
		if _, ok := ops[method]; !ok {
			t.Errorf("openapi.json documents %s but not for %s", path, r[1])
		}
	}
}
