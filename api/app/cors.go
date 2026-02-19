package app

import (
	"net/http"
	"strings"

	"go.sia.tech/jape"
)

type route struct {
	last bool
	sub  map[string]route
}

const wildcardParamSegment = ":param"

// mergeOptionsSub merges src into dst, unioning keys recursively.
func mergeOptionsSub(dst, src map[string]route) {
	for key, val := range src {
		if existing, ok := dst[key]; ok {
			if val.last {
				existing.last = true
				dst[key] = existing
			}
			mergeOptionsSub(existing.sub, val.sub)
		} else {
			dst[key] = val
		}
	}
}

// addOptionsPath inserts a route path into the tree, normalizing
// parameterized segments to a common wildcard key.
func addOptionsPath(m map[string]route, segments []string) {
	if len(segments) == 0 {
		return
	}
	seg := segments[0]
	if strings.HasPrefix(seg, ":") {
		seg = wildcardParamSegment
	}
	node, ok := m[seg]
	if !ok {
		node = route{sub: make(map[string]route)}
	}
	if len(segments) == 1 {
		node.last = true
	}
	m[seg] = node
	addOptionsPath(m[seg].sub, segments[1:])
}

// collectOptionsPaths resolves wildcard/static conflicts at each tree
// level by folding statics into the wildcard, then collects all leaf
// paths. httprouter does not allow a static and parameterized segment
// at the same position, so the wildcard is kept since it covers both.
func collectOptionsPaths(m map[string]route, prefix string) []string {
	// fold statics into wildcard at this level
	if wildcard, ok := m[wildcardParamSegment]; ok {
		for key, r := range m {
			if key == wildcardParamSegment {
				continue
			}
			if r.last {
				wildcard.last = true
			}
			mergeOptionsSub(wildcard.sub, r.sub)
			delete(m, key)
		}
		m[wildcardParamSegment] = wildcard
	}

	var paths []string
	for seg, r := range m {
		path := prefix + "/" + seg
		if r.last {
			paths = append(paths, path)
		}
		paths = append(paths, collectOptionsPaths(r.sub, path)...)
	}
	return paths
}

// corsMux is a helper that applies CORS middleware to the handlers in `enabledRoutes` and
// prevents OPTIONS handling in `disabledRoutes`.
//
// If a route is covered by both maps, it panics.
func corsMux(enabledRoutes map[string]jape.Handler, disabledRoutes map[string]jape.Handler) http.Handler {
	// adds the CORS headers globally since most endpoints are expected to be called from the browser.
	corsMiddleware := func(h jape.Handler) jape.Handler {
		return func(jc jape.Context) {
			jc.ResponseWriter.Header().Set("Access-Control-Allow-Origin", "*")
			jc.ResponseWriter.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
			jc.ResponseWriter.Header().Set("Access-Control-Allow-Headers", "*")
			h(jc)
		}
	}

	optionsHandler := corsMiddleware(func(jc jape.Context) {
		jc.ResponseWriter.WriteHeader(http.StatusNoContent)
	})

	disableOptionsHandler := func(jc jape.Context) {
		if jc.Request.Method == http.MethodOptions {
			jc.ResponseWriter.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	}

	optionsTree := make(map[string]route)
	disabledOptionsTree := make(map[string]route)
	routes := make(map[string]jape.Handler, len(enabledRoutes)+len(disabledRoutes))
	for key, h := range enabledRoutes {
		routes[key] = corsMiddleware(h)
		addOptionsPath(optionsTree, strings.Split(strings.Fields(key)[1], "/")[1:])
	}
	for _, path := range collectOptionsPaths(optionsTree, "") {
		routes["OPTIONS "+path] = optionsHandler
	}
	for key, h := range disabledRoutes {
		routes[key] = h
		addOptionsPath(disabledOptionsTree, strings.Split(strings.Fields(key)[1], "/")[1:])
	}
	// httprouter responds with a 200 by default. Add an explicit handler to reject it
	for _, path := range collectOptionsPaths(disabledOptionsTree, "") {
		optionsRoute := "OPTIONS " + path
		if _, ok := routes[optionsRoute]; ok {
			panic("disabled OPTIONS route " + path + " conflicts with a CORS-enabled OPTIONS route")
		}
		routes[optionsRoute] = disableOptionsHandler
	}
	return jape.Mux(routes)
}
