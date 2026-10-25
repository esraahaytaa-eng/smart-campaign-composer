package apidocs_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"smart-campaign-composer/internal/apidocs"
)

func get(path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	apidocs.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func spec(t *testing.T) map[string]any {
	t.Helper()
	rec := get("/docs/openapi.json")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("spec: want 200 application/json, got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var s map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	return s
}

func TestDocsPage_LoadsPinnedSwaggerUIUnderItsOwnCSP(t *testing.T) {
	rec := get("/docs")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("want 200 text/html, got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body, csp := rec.Body.String(), rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"swagger-ui-dist@5.17.14/swagger-ui-bundle.js", `integrity="sha384-`, "/docs/openapi.json"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, want := range []string{"script-src https://cdn.jsdelivr.net 'sha256-", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("want nosniff")
	}
}

func TestSpec_IsOpenAPI3WithCookieAuth(t *testing.T) {
	s := spec(t)
	if v, _ := s["openapi"].(string); !strings.HasPrefix(v, "3.") {
		t.Fatalf("openapi = %q, want 3.x", v)
	}
	schemes := s["components"].(map[string]any)["securitySchemes"].(map[string]any)
	cookie, _ := schemes["sessionCookie"].(map[string]any)
	if cookie["in"] != "cookie" || cookie["name"] != "scc_session" {
		t.Fatalf("sessionCookie scheme = %v", cookie)
	}
}

// Every API route the server registers is documented, and nothing else:
// routes are read from the source, where each is one "METHOD /api/..." string.
func TestSpec_MatchesRegisteredRoutes(t *testing.T) {
	files, err := filepath.Glob("../*/*.go")
	if err != nil {
		t.Fatal(err)
	}
	route := regexp.MustCompile(`"((?:GET|POST|PUT|DELETE) /api/[^"]*)"`)
	var registered []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range route.FindAllStringSubmatch(string(src), -1) {
			registered = append(registered, m[1])
		}
	}
	var documented []string
	for path, item := range spec(t)["paths"].(map[string]any) {
		for method := range item.(map[string]any) {
			if method != "parameters" {
				documented = append(documented, strings.ToUpper(method)+" "+path)
			}
		}
	}
	slices.Sort(registered)
	slices.Sort(documented)
	if len(registered) < 20 || !slices.Equal(registered, documented) {
		t.Fatalf("routes and spec differ\nregistered: %v\ndocumented: %v", registered, documented)
	}
}

// Each operation documents its answers, and every $ref points at a schema,
// response or parameter that exists.
func TestSpec_OperationsHaveResponsesAndRefsResolve(t *testing.T) {
	s := spec(t)
	for path, item := range s["paths"].(map[string]any) {
		for method, op := range item.(map[string]any) {
			if method == "parameters" {
				continue
			}
			if r, _ := op.(map[string]any)["responses"].(map[string]any); len(r) == 0 {
				t.Errorf("%s %s has no responses", method, path)
			}
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				var node any = s
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					m, _ := node.(map[string]any)
					node = m[part]
				}
				if node == nil {
					t.Errorf("unresolved $ref %s", ref)
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(s)
}
