package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	tmp := t.TempDir()
	accPath = filepath.Join(tmp, "accounts.json")
	forumPath = filepath.Join(tmp, "forum.json")
	loadAccounts()
	loadForum()
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

func TestForumProfilesAndInAppClientLogin(t *testing.T) {
	h := setupTest(t)

	// 1. HTML form registration redirects to /account and sets nsm_session cookie
	regForm := url.Values{
		"username": {"cravenz_dev"},
		"email":    {"dev@nsm.local"},
		"password": {"secretPass123"},
	}
	rReg := httptest.NewRequest("POST", "/register", strings.NewReader(regForm.Encode()))
	rReg.RemoteAddr = "127.0.0.1:12345"
	rReg.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rReg.Header.Set("Accept", "text/html")
	wReg := httptest.NewRecorder()
	h.ServeHTTP(wReg, rReg)
	if wReg.Code != http.StatusSeeOther || wReg.Header().Get("Location") != "/account" {
		t.Fatalf("expected 303 redirect to /account, got %d (%s)", wReg.Code, wReg.Header().Get("Location"))
	}
	var sessionCookie *http.Cookie
	for _, c := range wReg.Result().Cookies() {
		if c.Name == "nsm_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("missing nsm_session cookie on HTML registration")
	}

	// 2. Customize user profile (DisplayName, Title, Bio, colors, social metadata)
	profForm := url.Values{
		"display_name":    {"Cravenz Official"},
		"title":           {"Lead NSM Architect"},
		"bio":             {"Building the NSM multiplayer platform and Keymaster."},
		"avatar_color":    {"#e7064f"},
		"banner_color":    {"#1e293b"},
		"location":        {"Miami, FL"},
		"discord_tag":     {"cravenz#0001"},
		"favorite_server": {"NSM Official RP"},
	}
	rProf := httptest.NewRequest("POST", "/account/profile", strings.NewReader(profForm.Encode()))
	rProf.RemoteAddr = "127.0.0.1:12345"
	rProf.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rProf.AddCookie(sessionCookie)
	wProf := httptest.NewRecorder()
	h.ServeHTTP(wProf, rProf)
	if wProf.Code != 200 || !strings.Contains(wProf.Body.String(), "Cravenz Official") {
		t.Fatalf("profile update failed: %d", wProf.Code)
	}

	// Public profile page /u/cravenz_dev & PNG avatar /avatars/u/cravenz_dev/128.png
	if wU := request(h, "GET", "/u/cravenz_dev", ""); wU.Code != 200 || !strings.Contains(wU.Body.String(), "Lead NSM Architect") {
		t.Fatalf("public profile /u/cravenz_dev failed: %d", wU.Code)
	}
	if wAv := request(h, "GET", "/avatars/u/cravenz_dev/128.png", ""); wAv.Code != 200 || wAv.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("avatar PNG failed: %d (%s)", wAv.Code, wAv.Header().Get("Content-Type"))
	}

	// 3. Forum: view /forum, create topic, view topic, reply, and like
	if wF := request(h, "GET", "/forum", ""); wF.Code != 200 || !strings.Contains(wF.Body.String(), "NSM Community Forum") {
		t.Fatalf("GET /forum failed: %d", wF.Code)
	}
	topicForm := url.Values{
		"title":    {"My New NSM Resource Release v1.0"},
		"category": {"releases"},
		"tags":     {"release,hud"},
		"content":  {"Here is our brand new custom HUD resource for NSM servers!"},
	}
	rNewTopic := httptest.NewRequest("POST", "/forum/new", strings.NewReader(topicForm.Encode()))
	rNewTopic.RemoteAddr = "127.0.0.1:12345"
	rNewTopic.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rNewTopic.AddCookie(sessionCookie)
	wNewTopic := httptest.NewRecorder()
	h.ServeHTTP(wNewTopic, rNewTopic)
	if wNewTopic.Code != http.StatusSeeOther || !strings.HasPrefix(wNewTopic.Header().Get("Location"), "/forum/t/") {
		t.Fatalf("POST /forum/new failed: %d (%s)", wNewTopic.Code, wNewTopic.Header().Get("Location"))
	}
	topicLoc := wNewTopic.Header().Get("Location")
	if wT := request(h, "GET", topicLoc, ""); wT.Code != 200 || !strings.Contains(wT.Body.String(), "My New NSM Resource Release v1.0") {
		t.Fatalf("GET %s failed: %d", topicLoc, wT.Code)
	}

	// 4. NSM Client In-App Login (RSA User-Api-Key flow + /api/auth/client-poll + /l/api/validate/discourse)
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	nonce := "00112233445566778899aabbccddeeff"
	clientID := "deadbeefcafebabe0011223344556677"

	authForm := url.Values{
		"action":           {"authorize"},
		"client_id":        {clientID},
		"nonce":            {nonce},
		"public_key":       {pubPEM},
		"application_name": {"NSM Client"},
		"auth_redirect":    {"nsm://accept-auth"},
	}
	rAuth := httptest.NewRequest("POST", "/user-api-key/new", strings.NewReader(authForm.Encode()))
	rAuth.RemoteAddr = "127.0.0.1:12345"
	rAuth.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rAuth.AddCookie(sessionCookie)
	wAuth := httptest.NewRecorder()
	h.ServeHTTP(wAuth, rAuth)
	if wAuth.Code != 200 || !strings.Contains(wAuth.Body.String(), "nsm://accept-auth?payload=") {
		t.Fatalf("POST /user-api-key/new failed: %d", wAuth.Code)
	}

	// Client poller receives the RSA-encrypted payload
	wPoll := request(h, "POST", "/api/auth/client-poll", `{"nonce":"`+nonce+`","clientId":"`+clientID+`"}`)
	if wPoll.Code != 200 {
		t.Fatalf("POST /api/auth/client-poll failed: %d", wPoll.Code)
	}
	var pollResp struct {
		OK      bool   `json:"ok"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(wPoll.Body.Bytes(), &pollResp); err != nil || !pollResp.OK || pollResp.Payload == "" {
		t.Fatalf("unexpected poll response: %s", wPoll.Body.String())
	}

	cipherBytes, err := base64.StdEncoding.DecodeString(pollResp.Payload)
	if err != nil {
		t.Fatal(err)
	}
	plainBytes, err := rsa.DecryptPKCS1v15(rand.Reader, privKey, cipherBytes)
	if err != nil {
		t.Fatalf("RSA decrypt failed: %v", err)
	}
	var decPayload struct {
		Key   string `json:"key"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(plainBytes, &decPayload); err != nil || decPayload.Nonce != nonce || !strings.HasPrefix(decPayload.Key, "nsm_uapi_") {
		t.Fatalf("unexpected decrypted payload: %s", string(plainBytes))
	}

	// Verify /session/current.json and /l/api/validate/discourse with User-Api-Key
	wSess := requestAuth(h, "GET", "/session/current.json", "", map[string]string{"User-Api-Key": decPayload.Key})
	if wSess.Code != 200 || !strings.Contains(wSess.Body.String(), `"username":"cravenz_dev"`) {
		t.Fatalf("GET /session/current.json failed: %d %s", wSess.Code, wSess.Body.String())
	}
	wVal := requestAuth(h, "POST", "/l/api/validate/discourse", "", map[string]string{"User-Api-Key": decPayload.Key})
	if wVal.Code != 200 || !strings.Contains(wVal.Body.String(), `"username":"cravenz_dev"`) {
		t.Fatalf("POST /l/api/validate/discourse failed: %d %s", wVal.Code, wVal.Body.String())
	}

	// 5. Verify patchUIBundleBytes rewrites Discord invite to http://127.0.0.1, keeps root.html clean, and injects strict-mode-safe auth poller + interceptor into app/index.html
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f1, _ := zw.Create("app/index.html")
	f1.Write([]byte(`<html><head><script defer="defer" src="static/js/main.js?v=nsm078b"></script></head><body><div id="root"></div></body></html>`))
	f2, _ := zw.Create("app/static/js/main.js")
	f2.Write([]byte(`const BASE_URL = "https://discord.gg/UXj5QfCX88";`))
	f3, _ := zw.Create("root.html")
	f3.Write([]byte(`<html><head>` + uiDiscourseHeadInterceptorV2 + `</head><body></body></html>`))
	zw.Close()

	patchedZip, changed, err := patchUIBundleBytes(zbuf.Bytes())
	if err != nil || !changed {
		t.Fatalf("patchUIBundleBytes failed: changed=%v err=%v", changed, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(patchedZip), int64(len(patchedZip)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if zf.Name == "app/static/js/main.js" && !strings.Contains(string(b), `http://127.0.0.1`) {
			t.Fatalf("app/static/js/main.js was not rewritten: %s", string(b))
		}
		if zf.Name == "app/index.html" {
			htmlStr := string(b)
			for _, m := range []string{`id="nsm-client-auth-poll-v4"`, `id="nsm-discourse-interceptor"`, `src="static/js/main.js"`} {
				if !strings.Contains(htmlStr, m) {
					t.Fatalf("app/index.html missing %s: %s", m, htmlStr)
				}
			}
			for _, bad := range []string{`origFetch.call`, `?v=nsm078b`} {
				if strings.Contains(htmlStr, bad) {
					t.Fatalf("app/index.html must not contain %s: %s", bad, htmlStr)
				}
			}
		}
		if zf.Name == "root.html" && strings.Contains(string(b), "nsm-discourse-interceptor") {
			t.Fatalf("root.html must not contain nsm-discourse-interceptor: %s", string(b))
		}
	}
}

func TestDiscourseInAppLoginAndCravenzOwnerClaim(t *testing.T) {
	h := setupTest(t)

	// 1. Cravenz owner account is seeded with Pass == "" and initializes its password on first in-app /session login
	wCSRF := request(h, "GET", "/session/csrf.json", "")
	if wCSRF.Code != 200 || !strings.Contains(wCSRF.Body.String(), `"csrf"`) {
		t.Fatalf("GET /session/csrf.json failed: %d %s", wCSRF.Code, wCSRF.Body.String())
	}

	sessForm := url.Values{
		"login":    {"Cravenz"},
		"password": {"OwnerChosenPass!2026"},
	}
	rLogin := httptest.NewRequest("POST", "/session", strings.NewReader(sessForm.Encode()))
	rLogin.RemoteAddr = "127.0.0.1:12345"
	rLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wLogin := httptest.NewRecorder()
	h.ServeHTTP(wLogin, rLogin)
	if wLogin.Code != 200 || !strings.Contains(wLogin.Body.String(), `"username":"Cravenz"`) {
		t.Fatalf("POST /session for Cravenz failed: %d %s", wLogin.Code, wLogin.Body.String())
	}

	// 2. syntheticAuth() inside NSM.exe fetches /user-api-key/new and parses hidden inputs, then POSTs /user-api-key
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	nonce := "aabbccddeeff00112233445566778899"
	clientID := "11223344556677889900aabbccddeeff"

	qNew := url.Values{
		"scopes":           {"session_info,read,write"},
		"client_id":        {clientID},
		"nonce":            {nonce},
		"public_key":       {pubPEM},
		"application_name": {"NSM client"},
		"auth_redirect":    {"fivem://accept-auth"},
	}
	wNew := request(h, "GET", "/user-api-key/new?"+qNew.Encode(), "")
	if wNew.Code != 200 {
		t.Fatalf("GET /user-api-key/new failed: %d", wNew.Code)
	}
	for _, field := range []string{`name="authenticity_token"`, `name="application_name"`, `name="nonce"`, `name="client_id"`, `name="public_key"`, `name="scopes"`} {
		if !strings.Contains(wNew.Body.String(), field) {
			t.Fatalf("GET /user-api-key/new missing required DOM input %s", field)
		}
	}

	rKey := httptest.NewRequest("POST", "/user-api-key", strings.NewReader(qNew.Encode()))
	rKey.RemoteAddr = "127.0.0.1:12345"
	rKey.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wKey := httptest.NewRecorder()
	h.ServeHTTP(wKey, rKey)
	if wKey.Code != http.StatusFound {
		t.Fatalf("POST /user-api-key expected 302 Found, got %d (%s)", wKey.Code, wKey.Body.String())
	}
	loc := wKey.Header().Get("Location")
	uLoc, err := url.Parse(loc)
	if err != nil || uLoc.Query().Get("payload") == "" {
		t.Fatalf("POST /user-api-key redirect missing payload: %s", loc)
	}

	cipherBytes, err := base64.StdEncoding.DecodeString(uLoc.Query().Get("payload"))
	if err != nil {
		t.Fatal(err)
	}
	plainBytes, err := rsa.DecryptPKCS1v15(rand.Reader, privKey, cipherBytes)
	if err != nil {
		t.Fatalf("RSA decrypt failed: %v", err)
	}
	var dec struct {
		Key   string `json:"key"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(plainBytes, &dec); err != nil || dec.Nonce != nonce || !strings.HasPrefix(dec.Key, "nsm_uapi_") {
		t.Fatalf("unexpected decrypted payload: %s", string(plainBytes))
	}

	// 3. loadCurrentAccount(true) calls /session/current.json and /u/Cravenz.json (and /f/... variants)
	for _, p := range []string{"/session/current.json", "/f/session/current.json", "/u/Cravenz.json", "/f/u/Cravenz.json"} {
		w := requestAuth(h, "GET", p, "", map[string]string{"User-Api-Key": dec.Key, "User-Api-Client-Id": clientID})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"Cravenz"`) {
			t.Fatalf("GET %s failed: %d %s", p, w.Code, w.Body.String())
		}
	}

	// 4. Local owner can also reset/claim Cravenz via POST /account/claim-owner
	claimForm := url.Values{"password": {"UpdatedOwnerPass!2026"}}
	rClaim := httptest.NewRequest("POST", "/account/claim-owner", strings.NewReader(claimForm.Encode()))
	rClaim.RemoteAddr = "127.0.0.1:12345"
	rClaim.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rClaim.Header.Set("Accept", "application/json")
	wClaim := httptest.NewRecorder()
	h.ServeHTTP(wClaim, rClaim)
	if wClaim.Code != 200 || !strings.Contains(wClaim.Body.String(), `"role":"owner"`) {
		t.Fatalf("POST /account/claim-owner failed: %d %s", wClaim.Code, wClaim.Body.String())
	}

	// 5. Verify NSM & AFX multi-site ecosystem (/ and 6 standalone target="_blank" apps), Private Network Access CORS, and NSM/AFX logos
	wOpt := request(h, "OPTIONS", "/session/current.json", "")
	if wOpt.Header().Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf("OPTIONS /session/current.json missing Access-Control-Allow-Private-Network: true")
	}

	siteChecks := []struct {
		path   string
		marker string
	}{
		{"/", `id="nsm-mp-app"`},
		{"/servers", `<title>Server List - NSM</title>`},
		{"/login", `<title>Login - AFX Portal</title>`},
		{"/keymaster", `AFX <strong>Portal</strong>`},
		{"/support", `<title>AFX Support</title>`},
		{"/forum", `<title>AFX Forum - The home of NSM</title>`},
		{"/marketplace", `<title>AFX Marketplace</title>`},
		{"/docs", `<title>Home - NSM Documentation</title>`},
	}
	for _, sc := range siteChecks {
		wPage := request(h, "GET", sc.path, "")
		if wPage.Code != 200 {
			t.Fatalf("GET %s failed: %d", sc.path, wPage.Code)
		}
		body := wPage.Body.String()
		if !strings.Contains(body, sc.marker) {
			t.Fatalf("GET %s missing marker %q", sc.path, sc.marker)
		}
		for _, forbidden := range []string{"FiveM", "Cfx.re", "CitizenFX"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("GET %s contains forbidden string %q", sc.path, forbidden)
			}
		}
	}

	for _, imgPath := range []string{"/img/nsm-icon.png", "/img/afx-icon.png", "/img/nsm-afx-logo.png", "/img/hero-bg.jpg"} {
		wImg := request(h, "GET", imgPath, "")
		if wImg.Code != 200 || wImg.Body.Len() < 5000 {
			t.Fatalf("GET %s expected image bytes, got status %d len %d", imgPath, wImg.Code, wImg.Body.Len())
		}
	}

	// 11. Verify 1:1 Discourse Forum, Topic, and /u/<username> Profile DOM structure & same-tab navigation
	wForum := request(h, "GET", "/forum", "")
	if wForum.Code != http.StatusOK {
		t.Fatalf("GET /forum returned %d", wForum.Code)
	}
	forumBody := wForum.Body.String()
	for _, marker := range []string{
		`class="d-header"`,
		`id="d-sidebar"`,
		`class="categories-and-latest"`,
		`class="category-list"`,
		`class="subcategories"`,
		`class="latest-topic-list"`,
		`class="badge-category-bg"`,
	} {
		if !strings.Contains(forumBody, marker) {
			t.Fatalf("GET /forum missing Discourse marker %q", marker)
		}
	}
	if strings.Contains(forumBody, `target="_blank"`) {
		t.Fatalf("GET /forum still contains target=_blank link")
	}

	wTopic := request(h, "GET", "/forum/t/1", "")
	if wTopic.Code != http.StatusOK {
		t.Fatalf("GET /forum/t/1 returned %d", wTopic.Code)
	}
	topicBody := wTopic.Body.String()
	for _, marker := range []string{
		`id="topic-title"`,
		`class="container posts"`,
		`class="post-stream"`,
		`class="topic-map"`,
		`class="timeline-container"`,
		`id="reply-control"`,
		`id="suggested-topics"`,
	} {
		if !strings.Contains(topicBody, marker) {
			t.Fatalf("GET /forum/t/1 missing Discourse marker %q", marker)
		}
	}

	wProf := request(h, "GET", "/u/Cravenz", "")
	if wProf.Code != http.StatusOK {
		t.Fatalf("GET /u/Cravenz returned %d", wProf.Code)
	}
	profBody := wProf.Body.String()
	for _, marker := range []string{
		`user-full-themed-page fx-effect-aurora_waves`,
		`user-main user-themed-profile`,
		`class="about has-background"`,
		`user-profile-avatar deco-cat_ears`,
		`id="collapsed-info-panel"`,
		`class="user-primary-navigation"`,
		`class="top-section stats-section"`,
		`class="top-section replies-and-topics-section"`,
		`class="top-section top-categories-section"`,
		`class="top-section badges-section"`,
		`Badges (18/18)`,
		`AFX Pulse Eternal (Founder Lifetime)`,
		`d-social-community`,
		`d-social-github`,
		`github.com/platinum-cfx`,
	} {
		if !strings.Contains(profBody, marker) {
			t.Fatalf("GET /u/Cravenz missing Discourse profile marker %q", marker)
		}
	}
	if strings.Contains(profBody, `target="_blank"`) {
		t.Fatalf("GET /u/Cravenz still contains target=_blank link")
	}

	// 12. Verify every /u/Cravenz profile tab renders its own distinct Discourse view (not Summary on every tab)
	tabExpectations := []struct {
		url           string
		mustContain   string
		mustNotHave   string
	}{
		{"/u/Cravenz?tab=activity", `All Activity by`, `class="top-section stats-section"`},
		{"/u/Cravenz/activity", `class="user-navigation user-secondary-navigation"`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=topics", `Topics Created by`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=replies", `Replies by`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=read", `Recently Read Topics`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=likes", `Liked Posts &amp; Appreciated Contributions`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=solved", `Accepted Solutions`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=votes", `Server Boosts &amp; Topic Votes`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=notifications", `Notifications for`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=messages", `Personal Messages`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=badges", `Badges Earned by`, `class="top-section stats-section"`},
		{"/u/Cravenz?tab=pulse", `Supercharge Your NSM &amp; AFX Profile`, `class="top-section stats-section"`},
	}
	for _, tc := range tabExpectations {
		wTab := request(h, "GET", tc.url, "")
		if wTab.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", tc.url, wTab.Code)
		}
		tb := wTab.Body.String()
		if !strings.Contains(tb, tc.mustContain) {
			t.Fatalf("GET %s missing expected tab marker %q", tc.url, tc.mustContain)
		}
		if tc.mustNotHave != "" && strings.Contains(tb, tc.mustNotHave) {
			t.Fatalf("GET %s unexpectedly rendered Summary marker %q", tc.url, tc.mustNotHave)
		}
	}

	// 13. Verify Display Name, Full-Page Layout Colors, Animated Cat Ears & Moving Profile Effects update immediately
	customSess := registerTestUser(t, h, "theme_tester")
	formPayload := "display_name=Captain+Neon+AFX&title=Lead+Architect&theme_primary=%23EC4899&theme_accent=%238B5CF6&banner_color=%233B0764&avatar_color=%23F472B6&avatar_decoration=cat_ears&profile_effect=cyber_matrix&squad_house=brilliance&return_tab=summary"
	reqUpd := httptest.NewRequest("POST", "/account/profile", strings.NewReader(formPayload))
	reqUpd.RemoteAddr = "127.0.0.1:12345"
	reqUpd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUpd.Header.Set("X-NSM-Session", customSess)
	wUpd := httptest.NewRecorder()
	h.ServeHTTP(wUpd, reqUpd)
	if wUpd.Code != http.StatusOK {
		t.Fatalf("POST /account/profile expected 200 OK, got %d", wUpd.Code)
	}

	wCustomProf := requestAuth(h, "GET", "/u/theme_tester", "", map[string]string{"X-NSM-Session": customSess})
	if wCustomProf.Code != http.StatusOK {
		t.Fatalf("GET /u/theme_tester returned %d", wCustomProf.Code)
	}
	cpBody := wCustomProf.Body.String()
	for _, expected := range []string{
		`<span id="live-profile-display-name">Captain Neon AFX</span>`,
		`<span class="d-header-username" id="live-navbar-display-name">Captain Neon AFX</span>`,
		`user-full-themed-page fx-effect-cyber_matrix`,
		`user-profile-avatar deco-cat_ears`,
		`--prof-primary: #EC4899`,
		`--prof-accent: #8B5CF6`,
		`--prof-banner: #3B0764`,
		`--prof-avatar: #F472B6`,
		`Vanguard of Brilliance`,
	} {
		if !strings.Contains(cpBody, expected) {
			t.Fatalf("GET /u/theme_tester after profile update missing %q", expected)
		}
	}

	// Verify Preferences tab has custom NSM/AFX preset names and no "Nitro" naming
	wPrefTab := requestAuth(h, "GET", "/u/theme_tester?tab=preferences", "", map[string]string{"X-NSM-Session": customSess})
	prefBody := wPrefTab.Body.String()
	for _, presetName := range []string{"AFX Supernova", "Vinewood Horizon", "Vespucci Sunset", "Chiliad Sovereign"} {
		if !strings.Contains(prefBody, presetName) {
			t.Fatalf("Preferences tab missing custom preset %q", presetName)
		}
	}
	if strings.Contains(strings.ToLower(prefBody), "nitro") {
		t.Fatalf("Preferences tab still mentions 'nitro'")
	}

	// 14. Verify AFX Pulse subscription endpoint unlocks AFX Pulse Sovereign & Server Booster badge
	reqPulse := httptest.NewRequest("POST", "/account/pulse", strings.NewReader("tier=sovereign"))
	reqPulse.RemoteAddr = "127.0.0.1:12345"
	reqPulse.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPulse.Header.Set("X-NSM-Session", customSess)
	wPulse := httptest.NewRecorder()
	h.ServeHTTP(wPulse, reqPulse)
	if wPulse.Code != http.StatusOK {
		t.Fatalf("POST /account/pulse expected 200 OK, got %d", wPulse.Code)
	}
	if !strings.Contains(wPulse.Body.String(), "AFX Pulse Sovereign") {
		t.Fatalf("POST /account/pulse did not unlock AFX Pulse Sovereign")
	}

	// 15. Verify Staff user can grant Partner role & Partnered Server Owner badge to any user
	staffSess := registerTestUser(t, h, "mod_staff")
	accMu.Lock()
	staffUser := findUser("mod_staff")
	targetUser := findUser("theme_tester")
	if staffUser == nil || targetUser == nil {
		accMu.Unlock()
		t.Fatalf("expected mod_staff and theme_tester users to exist")
	}
	staffUser.Role = "staff"
	targetID := targetUser.ID
	accMu.Unlock()

	reqPartner := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/partner", targetID), strings.NewReader("action=grant"))
	reqPartner.RemoteAddr = "127.0.0.1:12345"
	reqPartner.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPartner.Header.Set("X-NSM-Session", staffSess)
	wPartner := httptest.NewRecorder()
	h.ServeHTTP(wPartner, reqPartner)
	if wPartner.Code != http.StatusOK {
		t.Fatalf("POST /admin/users/%d/partner by staff expected 200 OK, got %d: %s", targetID, wPartner.Code, wPartner.Body.String())
	}
	if !strings.Contains(wPartner.Body.String(), "Partnered Server Owner") {
		t.Fatalf("Partner role grant did not unlock Partnered Server Owner badge")
	}

	// Also verify the updated Display Name appears on the main website navbar (GET /) and users directory (GET /u)
	wHomeAuth := requestAuth(h, "GET", "/", "", map[string]string{"X-NSM-Session": customSess})
	if !strings.Contains(wHomeAuth.Body.String(), "Captain Neon AFX") {
		t.Fatalf("GET / navbar did not reflect updated DisplayName 'Captain Neon AFX'")
	}

	wUsersDir := request(h, "GET", "/u", "")
	if wUsersDir.Code != http.StatusOK || !strings.Contains(wUsersDir.Body.String(), "d-users-directory-table") {
		t.Fatalf("GET /u users directory failed: %d", wUsersDir.Code)
	}
	if !strings.Contains(wUsersDir.Body.String(), "Captain Neon AFX") {
		t.Fatalf("GET /u users directory did not reflect updated DisplayName 'Captain Neon AFX'")
	}

	ln, err := listenWithReuseAddr("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenWithReuseAddr failed: %v", err)
	}
	_ = ln.Close()
}





