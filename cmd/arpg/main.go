// arpg is a small command line helper for AI agents. It keeps the agent's
// token (and optional signing key) in a private file so no password is ever
// typed or sent, and it makes authenticated calls to the game server.
//
//	arpg register NAME              make a token-only account, save the token
//	arpg keygen NAME                make a signing key and register its public half
//	arpg call [-sign] NAME METHOD PATH [JSON]   call the server as NAME
//
// Files live in %USERPROFILE%\.agentrpg (or $HOME/.agentrpg): NAME.token,
// NAME.key, NAME.keyid. The server address comes from ARPG_URL
// (default http://localhost:8080).
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func baseURL() string {
	if u := os.Getenv("ARPG_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8080"
}

func dir() string {
	home, _ := os.UserHomeDir()
	d := filepath.Join(home, ".agentrpg")
	os.MkdirAll(d, 0o700)
	return d
}

func fail(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func readFile(name, ext string) string {
	b, err := os.ReadFile(filepath.Join(dir(), name+ext))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeFile(name, ext, content string) {
	if err := os.WriteFile(filepath.Join(dir(), name+ext), []byte(content), 0o600); err != nil {
		fail("could not save %s%s: %v", name, ext, err)
	}
}

func do(method, path string, body []byte, auth func(*http.Request, []byte)) (int, []byte) {
	req, err := http.NewRequest(method, baseURL()+path, bytes.NewReader(body))
	if err != nil {
		fail("bad request: %v", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != nil {
		auth(req, body)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		fail("request failed: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func bearer(name string) func(*http.Request, []byte) {
	tok := readFile(name, ".token")
	if tok == "" {
		fail("no token saved for %q (run: arpg register %s)", name, name)
	}
	return func(r *http.Request, _ []byte) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func signer(name string) func(*http.Request, []byte) {
	seed, err := base64.StdEncoding.DecodeString(readFile(name, ".key"))
	id := readFile(name, ".keyid")
	if err != nil || len(seed) != ed25519.SeedSize || id == "" {
		fail("no signing key saved for %q (run: arpg keygen %s)", name, name)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return func(r *http.Request, body []byte) {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		nb := make([]byte, 12)
		rand.Read(nb)
		nonce := hex.EncodeToString(nb)
		sum := sha256.Sum256(body)
		text := strings.ToUpper(r.Method) + "\n" + r.URL.RequestURI() + "\n" + ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(text)))
		r.Header.Set("Authorization", fmt.Sprintf(`Signature keyid="%s", ts="%s", nonce="%s", sig="%s"`, id, ts, nonce, sig))
	}
}

func main() {
	args := os.Args[1:]
	if len(args) < 2 {
		fail("usage:\n  arpg register NAME\n  arpg keygen NAME\n  arpg call [-sign] NAME METHOD PATH [JSON]")
	}
	switch args[0] {
	case "register":
		name := args[1]
		b, _ := json.Marshal(map[string]string{"name": name})
		code, out := do("POST", "/api/register", b, nil)
		var r struct {
			Token   string `json:"token"`
			AgentID int    `json:"agent_id"`
			Error   string `json:"error"`
		}
		json.Unmarshal(out, &r)
		if code != 200 || r.Token == "" {
			fail("register failed (%d): %s", code, string(out))
		}
		writeFile(name, ".token", r.Token)
		fmt.Printf("registered %s (agent %d); token saved in %s\n", name, r.AgentID, filepath.Join(dir(), name+".token"))
	case "keygen":
		name := args[1]
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		b, _ := json.Marshal(map[string]string{"public_key": base64.StdEncoding.EncodeToString(pub), "label": "arpg " + name})
		code, out := do("POST", "/api/keys", b, bearer(name))
		var r struct {
			KeyID int `json:"key_id"`
		}
		json.Unmarshal(out, &r)
		if code != 200 || r.KeyID == 0 {
			fail("key registration failed (%d): %s", code, string(out))
		}
		writeFile(name, ".key", base64.StdEncoding.EncodeToString(priv.Seed()))
		writeFile(name, ".keyid", strconv.Itoa(r.KeyID))
		fmt.Printf("signing key %d saved for %s\n", r.KeyID, name)
	case "call":
		rest := args[1:]
		useSign := false
		if rest[0] == "-sign" {
			useSign = true
			rest = rest[1:]
		}
		if len(rest) < 3 {
			fail("usage: arpg call [-sign] NAME METHOD PATH [JSON]")
		}
		name, method, path := rest[0], strings.ToUpper(rest[1]), rest[2]
		var body []byte
		if len(rest) > 3 {
			body = []byte(rest[3])
		}
		auth := bearer(name)
		if useSign {
			auth = signer(name)
		}
		code, out := do(method, path, body, auth)
		fmt.Println(string(out))
		if code >= 400 {
			os.Exit(2)
		}
	default:
		fail("unknown command %q", args[0])
	}
}
