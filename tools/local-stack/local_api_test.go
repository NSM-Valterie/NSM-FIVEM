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
	requireKeys = false
	keyClock = time.Now
	accPath = filepath.Join(t.TempDir(), "accounts.json")
	loadAccounts()
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

func requestAuth(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func registerTestUser(t *testing.T, h http.Handler, username string) string {
	t.Helper()
	w := requestAuth(h, "POST", "/api/auth/register",
		`{"Username":"`+username+`","Password":"Password123!","Email":"`+username+`@nsm.local"}`, nil)
	if w.Code != 200 {
		t.Fatalf("register %s failed: %d %s", username, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "nsm_session" {
			return c.Value
		}
	}
	t.Fatalf("missing nsm_session cookie for %s", username)
	return ""
}

func TestKeymasterFullMatrix(t *testing.T) {
	h := setupTest(t)
	now := time.Unix(1700000000, 0)
	keyClock = func() time.Time { return now }
	defer func() { keyClock = time.Now; requireKeys = false }()

	aliceSess := registerTestUser(t, h, "alice_srv")
	bobSess := registerTestUser(t, h, "bob_srv")

	// 1. Keymaster portal HTML is live (no longer a stub)
	wPage := requestAuth(h, "GET", "/keymaster", "", map[string]string{"X-NSM-Session": aliceSess})
	if wPage.Code != 200 || !strings.Contains(wPage.Body.String(), "Issue server key") {
		t.Fatalf("expected live keymaster portal, got %d %s", wPage.Code, wPage.Body.String())
	}

	// 2. Issue key for Alice and Bob
	wIssueA := requestAuth(h, "POST", "/api/keymaster/issue",
		`{"label":"Alice Main","ttlSeconds":60}`, map[string]string{"X-NSM-Session": aliceSess})
	if wIssueA.Code != 201 {
		t.Fatalf("issue Alice key: %d %s", wIssueA.Code, wIssueA.Body.String())
	}
	var issueRespA struct {
		ServerKey string                 `json:"serverKey"`
		Key       map[string]interface{} `json:"key"`
	}
	json.Unmarshal(wIssueA.Body.Bytes(), &issueRespA)
	aliceKey := issueRespA.ServerKey
	aliceKeyID := issueRespA.Key["id"].(string)

	wIssueB := requestAuth(h, "POST", "/api/keymaster/issue",
		`{"label":"Bob Main","ttlSeconds":3600}`, map[string]string{"X-NSM-Session": bobSess})
	var issueRespB struct {
		ServerKey string `json:"serverKey"`
	}
	json.Unmarshal(wIssueB.Body.Bytes(), &issueRespB)
	bobKey := issueRespB.ServerKey

	// 3. Valid key via sets sv_nsmServerKey in vars authenticates & strips secret from public listing
	hbWithVarsKey := `{"port":30120,"fallbackData":{"players":[],"info":{"resources":["chat"],"vars":{"sv_nsmServerKey":"` + aliceKey + `","sv_projectName":"Alice RP"}},"dynamic":{"clients":2,"svMaxclients":32,"hostname":"Alice RP Server"}}}`
	wHB := request(h, "POST", "/api/serverlist/ingress", hbWithVarsKey)
	if wHB.Code != 200 {
		t.Fatalf("valid key heartbeat failed: %d %s", wHB.Code, wHB.Body.String())
	}
	wSingle := request(h, "GET", "/api/servers/single/127.0.0.1:30120", "")
	if wSingle.Code != 200 || strings.Contains(wSingle.Body.String(), aliceKey) {
		t.Fatalf("single server missing or leaked secret: %d %s", wSingle.Code, wSingle.Body.String())
	}

	// 4. Unknown, malformed, and wrong-secret keys rejected (fail-closed)
	badKeys := []struct {
		key  string
		code int
	}{
		{"cfx_bad_prefix", 401},
		{"nsm_srv_0011223344556677.short", 401},
		{"nsm_srv_0011223344556677." + strings.Repeat("a", 48), 403},
		{aliceKeyID + "." + strings.Repeat("f", 48), 403},
	}
	for _, bk := range badKeys {
		wBad := requestAuth(h, "POST", "/api/serverlist/ingress", validHeartbeat,
			map[string]string{"Authorization": "Bearer " + bk.key})
		if wBad.Code != bk.code {
			t.Fatalf("bad key %s expected %d got %d (%s)", bk.key, bk.code, wBad.Code, wBad.Body.String())
		}
	}

	// 5. Cross-account IDOR revoke & endpoint hijack prevented
	wIDOR := requestAuth(h, "POST", "/api/keymaster/revoke",
		`{"keyId":"`+aliceKeyID+`"}`, map[string]string{"X-NSM-Session": bobSess})
	if wIDOR.Code != 403 {
		t.Fatalf("expected 403 on cross-account revoke, got %d", wIDOR.Code)
	}
	wHijack := requestAuth(h, "POST", "/api/serverlist/ingress", validHeartbeat,
		map[string]string{"Authorization": "Bearer " + bobKey})
	if wHijack.Code != 403 || !strings.Contains(wHijack.Body.String(), "endpoint_owned_by_another_account") {
		t.Fatalf("expected 403 on endpoint hijack, got %d %s", wHijack.Code, wHijack.Body.String())
	}

	// 6. Expired key rejected and active listing pruned
	now = now.Add(61 * time.Second)
	wExp := requestAuth(h, "POST", "/api/serverlist/ingress", validHeartbeat,
		map[string]string{"Authorization": "Bearer " + aliceKey})
	if wExp.Code != 403 || !strings.Contains(wExp.Body.String(), "expired_key") {
		t.Fatalf("expected 403 expired_key, got %d %s", wExp.Code, wExp.Body.String())
	}
	if wAfterExp := request(h, "GET", "/api/servers/single/127.0.0.1:30120", ""); wAfterExp.Code != 404 {
		t.Fatalf("expired key server was not pruned: %d", wAfterExp.Code)
	}

	// 7. Revoked key immediately evicts active server and rejects future heartbeats
	hbBob := strings.Replace(validHeartbeat, `"port":30120`, `"port":30125`, 1)
	if w := requestAuth(h, "POST", "/api/serverlist/ingress", hbBob, map[string]string{"Authorization": "Bearer " + bobKey}); w.Code != 200 {
		t.Fatalf("bob heartbeat: %d %s", w.Code, w.Body.String())
	}
	var bobKeyMeta struct {
		Key map[string]interface{} `json:"key"`
	}
	json.Unmarshal(wIssueB.Body.Bytes(), &bobKeyMeta)
	bobKeyID := bobKeyMeta.Key["id"].(string)
	wRev := requestAuth(h, "POST", "/api/keymaster/revoke",
		`{"keyId":"`+bobKeyID+`"}`, map[string]string{"X-NSM-Session": bobSess})
	if wRev.Code != 200 || !strings.Contains(wRev.Body.String(), `"evictedListings":1`) {
		t.Fatalf("revoke bob key failed: %d %s", wRev.Code, wRev.Body.String())
	}
	if w := request(h, "GET", "/api/servers/single/127.0.0.1:30125", ""); w.Code != 404 {
		t.Fatalf("revoked key server still listed: %d", w.Code)
	}
	if w := requestAuth(h, "POST", "/api/serverlist/ingress", hbBob, map[string]string{"Authorization": "Bearer " + bobKey}); w.Code != 403 {
		t.Fatalf("revoked key heartbeat should fail 403, got %d", w.Code)
	}

	// 8. requireKeys mode rejects keyless heartbeats fail-closed
	requireKeys = true
	if w := request(h, "POST", "/api/serverlist/ingress", validHeartbeat); w.Code != 401 {
		t.Fatalf("expected 401 when requireKeys=true and no key sent, got %d", w.Code)
	}
}

