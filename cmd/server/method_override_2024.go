package main

// A request a client sends as POST but means as DELETE, PATCH or PUT (feedback log 40: a GM's tool could not send DELETE, so clearing
// a pinned ask never reached the server). Some agents' HTTP tools send only GET and POST. The one rule, for every route: a POST with
// ?_method=DELETE (or PATCH, PUT) in its query, or the X-HTTP-Method-Override header, is served as that method. It is applied once,
// in withProtections, before anything reads the method; a signed request is still verified against the method the client sent
// (signedMethod). The reference says it on every DELETE, PATCH and PUT operation (tools/build_swagger.py), and messages that tell a
// player or GM to send DELETE name both forms with deleteHow.

import (
	"context"
	"net/http"
	"strings"
)

// methodOverrides: the methods a POST may stand for.
var methodOverrides = map[string]bool{http.MethodDelete: true, http.MethodPatch: true, http.MethodPut: true}

type sentMethodKey struct{}

// overrideMethod: the request as the method it stands for (unchanged unless it is a POST that names an allowed method).
func overrideMethod(r *http.Request) *http.Request {
	if r.Method != http.MethodPost {
		return r
	}
	m := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-HTTP-Method-Override")))
	if m == "" {
		m = strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("_method")))
	}
	if !methodOverrides[m] {
		return r
	}
	r = r.WithContext(context.WithValue(r.Context(), sentMethodKey{}, r.Method))
	r.Method = m
	return r
}

// signedMethod: the method the client sent (what a request signature covers), also when the request stands for another.
func signedMethod(r *http.Request) string {
	if m, ok := r.Context().Value(sentMethodKey{}).(string); ok {
		return m
	}
	return r.Method
}

// deleteHow: how to send a DELETE, for a message: the DELETE, and the POST form for tools that can't send DELETE.
func deleteHow(path, body string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return "DELETE " + path + " " + body + " (a tool that can't send DELETE: POST " + path + sep + "_method=DELETE with the same body)"
}
