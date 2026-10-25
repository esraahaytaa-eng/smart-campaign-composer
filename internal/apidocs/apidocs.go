// Package apidocs serves the API's OpenAPI description and a Swagger UI page
// for it at /docs (D73). The spec is written by hand; a test keeps it in step
// with the registered routes.
package apidocs

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"net/http"
)

//go:embed openapi.json
var spec []byte

// Swagger UI comes from a CDN at an exact version, checked by its SRI hash, so
// the project adds no dependency (D73).
const (
	cdn   = "https://cdn.jsdelivr.net"
	dist  = cdn + "/npm/swagger-ui-dist@5.17.14/"
	start = `SwaggerUIBundle({url: "/docs/openapi.json", dom_id: "#swagger-ui", withCredentials: true});`
	page  = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Smart Campaign Composer API</title>
<link rel="stylesheet" href="` + dist + `swagger-ui.css" integrity="sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn" crossorigin="anonymous">
</head>
<body>
<div id="swagger-ui"></div>
<script src="` + dist + `swagger-ui-bundle.js" integrity="sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep" crossorigin="anonymous"></script>
<script>` + start + `</script>
</body>
</html>
`
)

// The app's CSP (spa, D56) loosened for this page only: the CDN's files, the
// one inline start script by hash, and the inline styles Swagger UI sets.
var csp = func() string {
	sum := sha256.Sum256([]byte(start))
	return "default-src 'none'; script-src " + cdn + " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; " +
		"style-src " + cdn + " 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}()

// Routes registers GET /docs and GET /docs/openapi.json. Both are public: the
// spec holds no data, and the API itself still requires a session.
func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		w.Write([]byte(page))
	})
	mux.HandleFunc("GET /docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(spec)
	})
}
