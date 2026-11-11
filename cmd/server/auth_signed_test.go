package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
	"testing"
	"time"
)

func signFor(priv ed25519.PrivateKey, method, uri, ts, nonce string, body []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(signingString(method, uri, ts, nonce, body))))
}

func TestVerifySignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"a":1}`)
	sig := signFor(priv, "POST", "/api/x?y=1", ts, "abcdefgh12", body)

	if err := verifySignature(pub, "POST", "/api/x?y=1", ts, "abcdefgh12", body, sig, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if verifySignature(pub, "POST", "/api/x?y=1", ts, "abcdefgh12", []byte(`{"a":2}`), sig, now) == nil {
		t.Error("changed body must fail")
	}
	if verifySignature(pub, "GET", "/api/x?y=1", ts, "abcdefgh12", body, sig, now) == nil {
		t.Error("changed method must fail")
	}
	if verifySignature(pub, "POST", "/api/z", ts, "abcdefgh12", body, sig, now) == nil {
		t.Error("changed path must fail")
	}
	if verifySignature(pub, "POST", "/api/x?y=1", ts, "abcdefgh12", body, sig, now.Add(10*time.Minute)) == nil {
		t.Error("old timestamp must fail")
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if verifySignature(other, "POST", "/api/x?y=1", ts, "abcdefgh12", body, sig, now) == nil {
		t.Error("wrong key must fail")
	}
	if verifySignature(pub, "POST", "/api/x?y=1", ts, "short", body, signFor(priv, "POST", "/api/x?y=1", ts, "short", body), now) == nil {
		t.Error("short nonce must fail")
	}
}

func TestNonceUsedOnce(t *testing.T) {
	now := time.Now()
	if !useNonce("1:aaaaaaaa", now) {
		t.Fatal("first use should pass")
	}
	if useNonce("1:aaaaaaaa", now) {
		t.Fatal("replay must be refused")
	}
	if !useNonce("2:aaaaaaaa", now) {
		t.Fatal("same nonce on another key is allowed")
	}
}

func TestParseSignatureHeader(t *testing.T) {
	p := parseSignatureHeader(`Signature keyid="7", ts="1700000000", nonce="abc12345", sig="QQ=="`)
	if p["keyid"] != "7" || p["ts"] != "1700000000" || p["nonce"] != "abc12345" || p["sig"] != "QQ==" {
		t.Fatalf("bad parse: %v", p)
	}
}

func TestPasswordHashing(t *testing.T) {
	h, s := newPasswordHash("correct horse")
	if s != "" || !checkPassword("correct horse", h) {
		t.Error("bcrypt password should match")
	}
	if checkPassword("wrong", h) {
		t.Error("wrong password must fail")
	}
	if checkPassword("anything", "") {
		t.Error("token-only account (empty hash) must never match")
	}
	legacy := "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"
	if checkPassword("password", legacy) {
		t.Error("old SHA-256 hashes are no longer accepted")
	}
}

// testPasswordHash gives other tests a valid stored password hash.
func testPasswordHash(pw string) string {
	h, _ := newPasswordHash(pw)
	return h
}
