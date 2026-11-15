package main

// Reading a request's body: one rule for every request in the server (rework R3, Nov 15).
//
// Before, each handler read its body in its own way (about 200 reads): most ignored a field they didn't know, a third ignored
// a body that wasn't JSON at all, a few had their own unknown-field loops with five different replies, and sizes ran from 4 KB
// to 1 MB. Now every handler reads its body with readRequest (or gmRead for a GM command), which:
//   - reads the body once (up to maxRequestBody) and keeps it, so a wrapper can look at it afterwards (peekBody);
//   - needs one JSON object: an empty body is refused unless the request's fields are all optional (optionalBody), and
//     malformed JSON, a list or a value instead of an object, and anything after the object are refused;
//   - reads a field name as game.Key does ("Target-ID" is target_id), and refuses a field the request doesn't take, naming
//     every such field, the closest field it does take, and the full list (also in objects inside the body);
//   - refuses a value of the wrong type, naming the field and what to send;
//   - takes two envelope fields beside the request's own: an id that repeats the path's (character_id on
//     /api/characters/{id}/..., campaign_id on /api/campaigns/{id}/...) when it is the same id, and the campaign_id of a GM
//     command (gmRead), which picks the campaign;
//   - answers every problem in one shape: {"error", "message" (ending "Nothing was changed."), "fields"} with a 4xx status.
// A pending turn's steps are checked with the same rule (parseRequest) when they are sent, against the action request's
// own fields, not first when they resolve.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// bodySpec: how one request reads its body. Built from bodyOpts.
type bodySpec struct {
	optional bool                   // an empty body (or null) is read as an empty object: every field is optional
	extra    map[string]interface{} // envelope fields taken beside the request's own, decoded into these pointers
	fields   []string               // a map request: the fields it takes (structs list their own)
	anyKey   bool                   // a map request that checks its keys itself (character creation, the builder)
	limit    int64                  // a larger body than maxRequestBody (none today but the portrait, which reads raw bytes)
	aliases  map[string]string      // other names of a field (alias -> field), read as that field
	hint     string                 // added to an unknown-field refusal: where a misplaced field goes
	byTarget bool                   // gmRead: the command finds its campaign from its target; campaign_id is checked if sent
	path     string                 // the request's path (an id in it may be repeated in the body)
}

type bodyOpt func(*bodySpec)

// optionalBody: every field is optional, so an empty body (or none, or null) is an empty object.
func optionalBody() bodyOpt { return func(s *bodySpec) { s.optional = true } }

// alsoField takes one more field beside the request's own and decodes it into target (gmRead's campaign_id).
func alsoField(name string, target interface{}) bodyOpt {
	return func(s *bodySpec) {
		if s.extra == nil {
			s.extra = map[string]interface{}{}
		}
		s.extra[name] = target
	}
}

// onlyFields: the fields a map request takes.
func onlyFields(names ...string) bodyOpt {
	return func(s *bodySpec) { s.fields = append(s.fields, names...) }
}

// keysCheckedByHandler: a map request whose handler checks each key itself, with reasons the reader can't know (character
// creation's aliases and misplaced fields, the builder's steps). The reader still checks everything else.
func keysCheckedByHandler() bodyOpt { return func(s *bodySpec) { s.anyKey = true } }

// fieldAliases: other names a field may be sent under (alias -> field), read as the field (a feature's name for its pick).
func fieldAliases(m map[string]string) bodyOpt {
	return func(s *bodySpec) {
		if s.aliases == nil {
			s.aliases = map[string]string{}
		}
		for a, f := range m {
			s.aliases[a] = f
		}
	}
}

// campaignFromTarget: a GM command that finds its campaign from the character it acts on (gmReadAs). campaign_id is still
// taken, and must be a campaign the GM runs; when it isn't sent, the command uses the target's campaign.
func campaignFromTarget() bodyOpt { return func(s *bodySpec) { s.byTarget = true } }

// bodyHint is added to an unknown-field refusal: where a field that isn't this request's goes.
func bodyHint(h string) bodyOpt { return func(s *bodySpec) { s.hint = h } }

// bodyProblem: why a body was refused, and the reply that says so.
type bodyProblem struct {
	Status  int
	Code    string // unknown_field, invalid_json, body_required, wrong_type, id_mismatch, request_too_large
	Message string
	Fields  []string // the fields the request takes
	Unknown []string // unknown_field: the fields it doesn't take, as sent
}

// reply is the body of the refusal.
func (p *bodyProblem) reply() map[string]interface{} {
	fields := p.Fields
	if fields == nil {
		fields = []string{} // a request that takes no fields says so
	}
	out := map[string]interface{}{"error": p.Code, "message": p.Message, "fields": fields}
	if len(p.Unknown) > 0 {
		out["unknown"] = p.Unknown
	}
	return out
}

// writeBodyProblem answers a refused body.
func writeBodyProblem(w http.ResponseWriter, p *bodyProblem) {
	writeJSONStatus(w, p.Status, p.reply())
}

// readRequest reads the request's JSON body into v (a pointer to a struct, or to a map with onlyFields or
// keysCheckedByHandler). On a problem it answers 4xx and returns false; the handler then returns.
func readRequest(w http.ResponseWriter, r *http.Request, v interface{}, opts ...bodyOpt) bool {
	spec := bodySpec{}
	for _, o := range opts {
		o(&spec)
	}
	if r != nil && r.URL != nil {
		spec.path = r.URL.Path
	}
	raw, tooBig := bodyBytes(r, spec.limit)
	if tooBig {
		writeBodyProblem(w, &bodyProblem{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large",
			Message: "The request body is too large. Nothing was changed."})
		return false
	}
	if p := parseRequest(raw, v, spec); p != nil {
		writeBodyProblem(w, p)
		return false
	}
	return true
}

// noFields reads the body of a request that takes no fields: none, {} or null is fine; anything else is refused like an
// unknown field. On a problem it answers and returns false.
func noFields(w http.ResponseWriter, r *http.Request) bool {
	var none struct{}
	return readRequest(w, r, &none, optionalBody())
}

// bufferedBody is a request body that has been read: its bytes stay with the request, so a wrapper (reminders, logging,
// the signature check) and the handler can each read it.
type bufferedBody struct {
	*bytes.Reader
	raw    []byte
	tooBig bool
}

func (b *bufferedBody) Close() error { return nil }

// bodyBytes reads the body once (up to maxRequestBody, or limit when larger) and keeps it on the request.
func bodyBytes(r *http.Request, limit int64) ([]byte, bool) {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return nil, false
	}
	if limit < maxRequestBody {
		limit = maxRequestBody
	}
	if b, ok := r.Body.(*bufferedBody); ok {
		b.Reset(b.raw)
		return b.raw, b.tooBig || int64(len(b.raw)) > limit
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	r.Body.Close()
	tooBig := int64(len(raw)) > limit
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		tooBig = true
	}
	if tooBig {
		raw = nil
	}
	r.Body = &bufferedBody{Reader: bytes.NewReader(raw), raw: raw, tooBig: tooBig}
	return raw, tooBig
}

// peekBody: the body's bytes, leaving it to be read again (for wrappers that look at a field of the request).
func peekBody(r *http.Request) []byte {
	raw, _ := bodyBytes(r, 0)
	return raw
}

// pathIDs: the ids a request's path names, which its body may repeat (/api/characters/7/... is character 7).
var pathIDPattern = regexp.MustCompile(`^/api/(characters|campaigns)/([0-9]+)(/|$)`)

func pathIDs(path string) map[string]int {
	m := pathIDPattern.FindStringSubmatch(path)
	if m == nil {
		return nil
	}
	id, _ := strconv.Atoi(m[2])
	if m[1] == "characters" {
		return map[string]int{"character_id": id}
	}
	return map[string]int{"campaign_id": id}
}

// parseRequest is the rule on a body already read (readRequest's, and a pending turn's steps): nil when v holds it.
func parseRequest(raw []byte, v interface{}, spec bodySpec) *bodyProblem {
	isMap := false
	t := reflect.TypeOf(v)
	if t != nil && t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Map {
		isMap = true
	}
	var own []string // the request's own fields
	if isMap {
		own = append(own, spec.fields...)
	} else {
		own = requestFields(v)
	}
	ids := pathIDs(spec.path)
	shown := append([]string{}, own...) // the fields the refusal lists: the request's own and its envelope
	for name := range spec.extra {
		if !hasString(shown, name) {
			shown = append(shown, name)
		}
	}
	sortFields(shown)
	nothing := " Nothing was changed."
	fail := func(status int, code, msg string) *bodyProblem {
		return &bodyProblem{Status: status, Code: code, Message: msg + nothing, Fields: shown}
	}
	takes := "this request takes no fields"
	if len(shown) > 0 {
		takes = "this request takes " + strings.Join(shown, ", ")
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		if spec.optional {
			return nil
		}
		return fail(http.StatusBadRequest, "body_required", "Send a JSON object as the body: "+takes+".")
	}
	if trimmed[0] != '{' {
		what := "a JSON value"
		switch trimmed[0] {
		case '[':
			what = "a list"
		case '"':
			what = "a string"
		}
		return fail(http.StatusBadRequest, "invalid_json", fmt.Sprintf("The body must be one JSON object, not %s: %s.", what, takes))
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	top := map[string]json.RawMessage{}
	var order []string // the keys as sent, in order
	if err := decodeObjectKeys(dec, top, &order); err != nil {
		return fail(http.StatusBadRequest, "invalid_json", fmt.Sprintf("The body isn't valid JSON (%s): send one JSON object; %s.", jsonErrorText(err), takes))
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail(http.StatusBadRequest, "invalid_json", "There is more after the JSON object: send one object; "+takes+".")
	}

	// each key is a field of the request, an envelope field, or unknown; a field name is read as game.Key reads it
	byKey := map[string]string{}
	for a, f := range spec.aliases {
		if hasString(own, f) {
			byKey[game.Key(a)] = f
		}
	}
	for _, f := range own {
		byKey[game.Key(f)] = f
	}
	normalized := map[string]json.RawMessage{}
	var unknown []string
	seen := map[string]string{} // field -> the key it was sent as
	for _, k := range order {
		val := top[k]
		name, ok := byKey[game.Key(k)]
		if !ok {
			for e := range spec.extra {
				if game.Key(e) == game.Key(k) {
					name, ok = e, true
				}
			}
		}
		if !ok {
			for e, id := range ids {
				if game.Key(e) == game.Key(k) {
					var sent int
					if json.Unmarshal(val, &sent) != nil {
						return fail(http.StatusBadRequest, "wrong_type", fmt.Sprintf("%s has the wrong type: send a whole number (the id in the path, %d), or leave it out.", e, id))
					}
					if sent != id {
						return fail(http.StatusBadRequest, "id_mismatch", fmt.Sprintf("%s %d is not the one in the path (%d): leave %s out, or send %d.", e, sent, id, e, id))
					}
					name, ok = "", true // the same id as the path's: nothing more to do with it
				}
			}
			if ok && name == "" {
				continue
			}
		}
		if !ok {
			if isMap && spec.anyKey {
				normalized[k] = val
				continue
			}
			unknown = append(unknown, k)
			continue
		}
		if prev, dup := seen[name]; dup && prev != k {
			return fail(http.StatusBadRequest, "invalid_json", fmt.Sprintf("%q and %q are the same field (%s): send it once.", prev, k, name))
		}
		seen[name] = k
		if target, isExtra := spec.extra[name]; isExtra {
			if err := json.Unmarshal(val, target); err != nil {
				return fail(http.StatusBadRequest, "wrong_type", fmt.Sprintf("%s has the wrong type: %s.", name, wrongTypeAdvice(err)))
			}
			if !hasString(own, name) {
				continue
			}
		}
		normalized[name] = val
	}
	if len(unknown) > 0 {
		var parts []string
		for _, u := range unknown {
			part := fmt.Sprintf("%q", u)
			if c := closestField(u, shown); c != "" {
				part += fmt.Sprintf(" (did you mean %q?)", c)
			}
			parts = append(parts, part)
		}
		word := "Unknown field "
		if len(unknown) > 1 {
			word = "Unknown fields "
		}
		msg := word + strings.Join(parts, ", ") + ": " + takes + "."
		if spec.hint != "" {
			msg += " " + spec.hint
		}
		p := fail(http.StatusBadRequest, "unknown_field", msg)
		p.Unknown = unknown
		return p
	}

	// decode the fields into the request; a field inside an object of the body is checked against that object's fields
	buf, _ := json.Marshal(normalized)
	d := json.NewDecoder(bytes.NewReader(buf))
	if !isMap {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(v); err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "json: unknown field ") {
			bad := strings.Trim(strings.TrimPrefix(msg, "json: unknown field "), `"`)
			inner := nestedFields(v)
			text := fmt.Sprintf("Unknown field %q in an object inside the body", bad)
			if c := closestField(bad, inner); c != "" {
				text += fmt.Sprintf(" (did you mean %q?)", c)
			}
			p := fail(http.StatusBadRequest, "unknown_field", text+": "+takes+".")
			p.Unknown = []string{bad}
			return p
		}
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			field := te.Field
			if field == "" {
				field = "the body"
			}
			return fail(http.StatusBadRequest, "wrong_type", fmt.Sprintf("%s has the wrong type: %s.", field, wrongTypeAdvice(err)))
		}
		return fail(http.StatusBadRequest, "invalid_json", fmt.Sprintf("The body could not be read (%s): %s.", jsonErrorText(err), takes))
	}
	return nil
}

// decodeObjectKeys reads one JSON object into m, keeping the order its keys came in.
func decodeObjectKeys(dec *json.Decoder, m map[string]json.RawMessage, order *[]string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("not an object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		k, ok := tok.(string)
		if !ok {
			return fmt.Errorf("a key must be a string")
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return err
		}
		if _, dup := m[k]; !dup {
			*order = append(*order, k)
		}
		m[k] = val
	}
	_, err = dec.Token() // the closing brace
	return err
}

// jsonErrorText: the decoder's error without its "json: " prefix ("unexpected EOF" for a cut-off body).
func jsonErrorText(err error) string {
	msg := strings.TrimPrefix(err.Error(), "json: ")
	if errors.Is(err, io.ErrUnexpectedEOF) || msg == "EOF" {
		return "it ends too soon"
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("%s, at character %d", msg, se.Offset)
	}
	return msg
}

// wrongTypeAdvice: what to send instead of a value of the wrong type.
func wrongTypeAdvice(err error) string {
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return jsonErrorText(err)
	}
	want := "a value of type " + te.Type.String()
	switch k := te.Type.Kind(); {
	case k >= reflect.Int && k <= reflect.Uint64:
		want = "a whole number"
	case k == reflect.Float32 || k == reflect.Float64:
		want = "a number"
	case k == reflect.String:
		want = "text in quotes"
	case k == reflect.Bool:
		want = "true or false"
	case k == reflect.Slice || k == reflect.Array:
		want = "a list"
	case k == reflect.Struct || k == reflect.Map:
		want = "an object"
	}
	sent := te.Value
	switch {
	case sent == "string":
		sent = "text"
	case strings.HasPrefix(sent, "number"):
		sent = "a " + sent
	case sent == "array":
		sent = "a list"
	case sent == "object":
		sent = "an object"
	case sent == "bool":
		sent = "true or false"
	}
	return fmt.Sprintf("send %s (you sent %s)", want, sent)
}

// closestField: the field a misspelled one probably means: the same name as game.Key reads it without its underscores
// ("targetId"), else the nearest by spelling ("targt": target_id), counting a field's first word on its own and an
// abbreviation made of the field's letters in order ("qty": quantity); "" when none is near enough.
func closestField(bad string, fields []string) string {
	k := game.Key(bad)
	squash := strings.ReplaceAll(k, "_", "")
	best, bestScore := "", 1<<30
	limit := len(squash) / 3
	if limit < 2 {
		limit = 2
	}
	for _, f := range fields {
		fk := game.Key(f)
		if strings.ReplaceAll(fk, "_", "") == squash {
			return f
		}
		score := editDistance(k, fk)
		if first := strings.SplitN(fk, "_", 2)[0]; first != fk {
			if d := editDistance(k, first) + 1; d < score {
				score = d
			}
		}
		if len(squash) >= 2 && squash[0] == fk[0] && isSubsequence(squash, strings.ReplaceAll(fk, "_", "")) && score > limit {
			score = limit
		}
		if score <= limit && (score < bestScore || score == bestScore && len(f) < len(best)) {
			best, bestScore = f, score
		}
	}
	return best
}

// isSubsequence: every letter of a appears in b, in order.
func isSubsequence(a, b string) bool {
	i := 0
	for j := 0; j < len(b) && i < len(a); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}

// requestFields lists the JSON field names of a request struct (v is a pointer to it), with the fields of embedded structs.
func requestFields(v interface{}) []string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	structFields(t, func(name string, _ reflect.Type) { out = append(out, name) })
	sortFields(out)
	return out
}

// structFields calls f with the JSON name and type of each field of struct type t, as encoding/json reads them.
func structFields(t reflect.Type, f func(string, reflect.Type)) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" && !strings.HasPrefix(tag, "-,") {
			continue
		}
		if sf.Anonymous && name == "" {
			et := sf.Type
			if et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				structFields(et, f)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		f(name, sf.Type)
	}
}

// nestedFields: the field names of the objects inside a request (for a misspelled field inside one).
func nestedFields(v interface{}) []string {
	seen := map[reflect.Type]bool{}
	var out []string
	var walk func(t reflect.Type, top bool)
	walk = func(t reflect.Type, top bool) {
		for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		structFields(t, func(name string, ft reflect.Type) {
			if !top {
				out = append(out, name)
			}
			walk(ft, false)
		})
	}
	if t := reflect.TypeOf(v); t != nil {
		walk(t, true)
	}
	sort.Strings(out)
	return out
}

// keysNotIn: the keys of a map request that aren't among its fields, as sent (the "unknown" of a refusal written by a handler
// that checks its own keys).
func keysNotIn(m map[string]interface{}, fields []string) []string {
	var out []string
	for k := range m {
		if !hasString(fields, k) {
			out = append(out, k)
		}
	}
	sortFields(out)
	return out
}

// sortFields sorts field names as a reader would look them up (case aside).
func sortFields(l []string) {
	sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i]) < strings.ToLower(l[j]) })
}

func hasString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
