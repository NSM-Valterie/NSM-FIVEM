package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTest(t *testing.T) http.Handler {
	t.Helper()
	reg = newRegistry()
	state = ownerState{Servers: map[string]serverMods{}, FeedIDs: map[string]bool{}}
	ticketPublicKey = ""
	initSite()
	return localHandler()
}
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestRouteContracts(t *testing.T) {
	h := setupTest(t)
	cases := []struct {
		method, path string
		code         int
		body         string
	}{
		{"GET", "/api/identities", 200, `{"identities":[]}`},
		{"POST", "/api/ticket/identities", 200, `{"identities":[]}`},
		{"GET", "/api/ticket/identities", 200, `{"identities":[]}`},
		{"POST", "/api/identities", 503, `identity linking`},
		{"GET", "/v1/public/featured-servers", 200, `"pinnedServers":[]`},
		{"GET", "/v1/public/tweets", 200, `[]`},
		{"GET", "/v1/public/notices/fivem", 200, `[]`},
		{"GET", "/v1/public/pool-size-limits/fivem", 200, `{}`},
		{"GET", "/runtime/counts.json", 200, `[0,null,null]`},
		{"GET", "/api/servers/streamRedir/", 200, ``},
		{"GET", "/api/servers/streamRedir", 200, ``},
		{"GET", "/api/servers/single/127.0.0.1:30120", 404, `{}`},
		{"GET", "/api/servers/icon/127.0.0.1:30120/0.png", 404, ``},
		{"GET", "/api/ticket/pubkey", 503, `no trusted public key`},
		{"GET", "/api/upvote/", 403, `unavailable`},
		{"POST", "/api/upvote/", 403, `unavailable`},
		{"GET", "/api/serverlist/ingress", 405, `POST required`},
		{"POST", "/v1/public/tweets", 405, `method not allowed`},
		{"OPTIONS", "/api/identities", 204, ``},
		{"GET", "/health", 200, `"version":"0.7.7-local"`},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			w := request(h, c.method, c.path, "")
			if w.Code != c.code || !strings.Contains(w.Body.String(), c.body) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatal("CORS missing")
			}
			if w.Header().Get("X-NSM-Backend") != backendVersion {
				t.Fatal("version missing")
			}
		})
	}
}

const validHeartbeat = `{"port":30120,"listingToken":"","ipOverride":"","private":true,"fallbackData":{"players":[],"info":{"info":{"resources":["chat"],"version":"test-fixture","vars":{"sv_projectName":"Integration test fixture"}}},"dynamic":{"dynamic":{"clients":2,"svMaxclients":48,"hostname":"TEST FIXTURE - NOT A LIVE GAME SERVER"}}}}`

func TestHeartbeatStreamAndExpiry(t *testing.T) {
	h := setupTest(t)
	if w := request(h, "POST", "/api/serverlist/ingress", validHeartbeat); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := request(h, "GET", "/api/servers/streamRedir/", "")
	b := w.Body.Bytes()
	if len(b) < 4 || int(binary.LittleEndian.Uint32(b)) != len(b)-4 {
		t.Fatal("invalid frame length")
	}
	if !bytes.Contains(b, []byte("127.0.0.1:30120")) || !bytes.Contains(b, []byte("gta5")) {
		t.Fatal("frame fields missing")
	}
	w = request(h, "GET", "/api/servers/single/127.0.0.1:30120", "")
	var server struct {
		EndPoint string
		Data     ServerData
	}
	if json.Unmarshal(w.Body.Bytes(), &server) != nil || server.Data.Clients != 2 || len(server.Data.ConnectEndPoints) != 1 {
		t.Fatal(w.Body.String())
	}
	if w = request(h, "GET", "/runtime/counts.json", ""); w.Body.String() != "[2,null,null]" {
		t.Fatal(w.Body.String())
	}
	reg.mu.Lock()
	reg.servers[server.EndPoint].last = time.Now().Add(-heartbeatTTL - time.Second)
	reg.mu.Unlock()
	if w = request(h, "GET", "/api/servers/single/127.0.0.1:30120", ""); w.Code != 404 {
		t.Fatal("stale single")
	}
	if w = request(h, "GET", "/api/servers/streamRedir/", ""); w.Body.Len() != 0 {
		t.Fatal("stale stream")
	}
	if len(reg.servers) != 0 {
		t.Fatal("stale record not pruned")
	}
}
func TestMalformedHeartbeats(t *testing.T) {
	h := setupTest(t)
	for _, body := range []string{`{}`, `null`, `[]`, `{`, `{} {}`, `{"port":-1}`, `{"port":65536}`, `{"ipOverride":"8.8.8.8"}`, `{"fallbackData":{"info":[]}}`, `{"fallbackData":{"dynamic":{"clients":-1}}}`, `{"fallbackData":{"dynamic":{"clients":1.5}}}`, `{"fallbackData":{"dynamic":{"clients":2147483648}}}`, `{"fallbackData":{"info":{"resources":[42]}}}`, `{"fallbackData":{"info":{"vars":[]}}}`, `{"fallbackData":{"players":{}}}`} {
		w := request(h, "POST", "/api/serverlist/ingress", body)
		if w.Code < 400 {
			t.Errorf("accepted %s", body)
		}
	}
	if len(reg.live()) != 0 {
		t.Fatal("invalid entry stored")
	}
	w := request(h, "POST", "/api/serverlist/ingress", `{"x":"`+strings.Repeat("a", 1<<20)+`"}`)
	if w.Code != 413 {
		t.Fatalf("oversize %d", w.Code)
	}
}
func TestRealCountsNotFabricated(t *testing.T) {
	h := setupTest(t)
	request(h, "POST", "/api/serverlist/ingress", validHeartbeat)
	state.Servers["127.0.0.1:30120"] = serverMods{FakePlayerCnt: 9000}
	w := request(h, "GET", "/runtime/counts.json", "")
	if w.Body.String() != "[2,null,null]" {
		t.Fatal(w.Body.String())
	}
}
func TestProxyDoesNotCacheCredentials(t *testing.T) {
	setupTest(t)
	dir := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("credential-response")) }))
	defer upstream.Close()
	r := httptest.NewRequest("GET", "/l/api/validate/entitlement/rosfive", nil)
	w := httptest.NewRecorder()
	serveProxy(w, r, "l", upstream.URL)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err := os.Stat(cachePath("l", "api/validate/entitlement/rosfive", "")); !os.IsNotExist(err) {
		t.Fatal("credentials cached")
	}
}
func TestExistingCacheStillWorks(t *testing.T) {
	setupTest(t)
	dir := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("game-file")) }))
	for i := 0; i < 2; i++ {
		if i == 1 {
			upstream.Close()
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/c/mirrors/file.bin", nil)
		serveProxy(w, r, "c", upstream.URL)
		if w.Code != 200 || w.Body.String() != "game-file" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	files, _ := filepath.Glob("mirror-cache/c/*")
	if len(files) != 1 {
		t.Fatal(files)
	}
}
func TestUnavailableUpstream(t *testing.T) {
	setupTest(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/l/test", nil)
	serveProxy(w, r, "l", "http://127.0.0.1:1")
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
}
func TestSpoofedForwardingIgnored(t *testing.T) {
	h := setupTest(t)
	r := httptest.NewRequest("POST", "/api/serverlist/ingress", strings.NewReader(validHeartbeat))
	r.RemoteAddr = "127.0.0.1:20"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if reg.get("127.0.0.1:30120") == nil {
		t.Fatal("spoofed endpoint")
	}
}
func TestFullHTTPPipeline(t *testing.T) {
	h := setupTest(t)
	s := httptest.NewServer(h)
	defer s.Close()
	resp, err := http.Post(s.URL+"/api/serverlist/ingress", "application/json", strings.NewReader(validHeartbeat))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = http.Get(s.URL + "/api/servers/streamRedir/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if len(b) < 4 {
		t.Fatal("empty HTTP stream")
	}
}

func TestTrustedPublicKeyOnly(t *testing.T) {
	h := setupTest(t)
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	path := filepath.Join(t.TempDir(), "public.pem")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: der}), 0600)
	ticketPublicKey, err = readTicketPublicKey(path)
	if err != nil {
		t.Fatal(err)
	}
	w := request(h, "GET", "/api/ticket/pubkey", "")
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result["key"] != base64.StdEncoding.EncodeToString(der) {
		t.Fatal(w.Code, w.Body.String())
	}
	os.WriteFile(path, []byte("invalid key"), 0600)
	if _, err = readTicketPublicKey(path); err == nil {
		t.Fatal("invalid public key accepted")
	}
}
func TestNonLocalRequestRejected(t *testing.T) {
	h := setupTest(t)
	r := httptest.NewRequest("GET", "/api/servers/streamRedir/", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
