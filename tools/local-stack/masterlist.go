// NSM masterlist service v2
// =========================
// Standalone replacement for the Cfx masterlist endpoints used by NSM
// clients (v3+) and servers (v6+). Single file, standard library only.
//
// Build:
//
//	go build -o nsm-masterlist .
//	GOOS=windows GOARCH=amd64 go build -o NSM-Masterlist.exe .
//	GOOS=linux GOARCH=amd64 go build -o nsm-masterlist-linux-amd64 .
//	GOOS=linux GOARCH=arm64 go build -o nsm-masterlist-linux-arm64 .
//
// Endpoints (all on one port, default 80):
//
//	GET  /api/servers/streamRedir/     full list: [4-byte LE length][protobuf master.Server] frames
//	GET  /api/servers/single/<ip:port> single server JSON {EndPoint, Data}
//	GET  /api/servers/icon/...         server icon (404 -> client uses default)
//	POST /api/serverlist/ingress       server heartbeat (GameServer.cpp)
//	GET  /api/ticket/pubkey            RSA public key (unused with sv_lan true)
//	GET  /api/identities               player identities (empty)
//	GET  /api/ticket/identities        ticket identities (empty)
//	GET  /v1/public/featured-servers   featured servers (empty)
//	GET  /v1/public/notices/fivem      notices (empty)
//	GET  /v1/public/tweets             official feed (Twitter-format JSON array;
//	                                   sources: owner posts + Discord poller)
//	GET  /v1/public/pool-size-limits/* pool limits (empty = unlimited)
//	GET  /runtime/counts.json          player counts [n, 0, n]
//	*    /l/api/validate/entitlement*  unchanged upstream validation; never fabricated
//	*    /f/...                        forum mimic (posts.json, upload sink)
//
// Owner admin API (Authorization: Bearer $OWNER_TOKEN, owner-only):
//
//	POST /admin/upvotes      {"endpoints":[...], "set":N} or {"add":N}
//	POST /admin/fakeplayers  {"endpoints":[...], "count":N}  (0..9000, 0=off)
//	POST /admin/feed         {"text":"...", "author":"...", "screenName":"...",
//	                          "avatar":"...", "url":"..."}   (owner post)
//	POST /admin/feed/import  {"items":[{id,text,author,screenName,avatar,url,
//	                          createdAt,media:[{url,w,h}]}]}  (poller, id-deduped)
//	POST /admin/feed/delete  {"id":"..."}
//	GET  /admin/state        current mods + feed
//
// Entries expire 5 minutes after their last heartbeat. Owner mods (upvotes,
// legacy fake-player settings are ignored in local-first mode.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	heartbeatTTL = 300 * time.Second
	gamename     = "gta5"
)

// ---------------------------------------------------------------------------
// protobuf encoding (schema extracted from the cfx-ui worker bundle:
// master.Server { string EndPoint = 1; master.ServerData Data = 2; }
// master.ServerData {
//   int32 svMaxclients = 1; int32 clients = 2; int32 protocol = 3;
//   string hostname = 4; string gametype = 5; string mapname = 6;
//   repeated string resources = 8; string server = 9;
//   repeated master.Player players = 10; int32 iconVersion = 11;
//   map<string,string> vars = 12; bool enhancedHostSupport = 16;
//   int32 upvotePower = 17; repeated string connectEndPoints = 18;
//   int32 burstPower = 19; }
// master.Player { string name = 1; repeated string identifiers = 2;
//   string endpoint = 3; int32 ping = 4; int32 id = 5; })
// ---------------------------------------------------------------------------

func pbVarint(n uint64) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func pbTag(field, wire int) []byte { return pbVarint(uint64(field<<3 | wire)) }

func pbString(field int, s string) []byte {
	b := []byte(s)
	out := pbTag(field, 2)
	out = append(out, pbVarint(uint64(len(b)))...)
	return append(out, b...)
}

func pbInt(field int, v int64) []byte {
	if v == 0 {
		return nil // protobuf default, decoder yields 0
	}
	out := pbTag(field, 0)
	return append(out, pbVarint(uint64(v))...)
}

func pbMessage(field int, payload []byte) []byte {
	out := pbTag(field, 2)
	out = append(out, pbVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

// Player is the per-player entry served in ServerData.players (field 10).
// Identifiers are always omitted: we never fabricate player identifiers.
type Player struct {
	Name string `json:"name"`
	Ping int64  `json:"ping,omitempty"`
	ID   int64  `json:"id,omitempty"`
}

func encodePlayer(p *Player) []byte {
	var out []byte
	if p.Name != "" {
		out = append(out, pbString(1, p.Name)...)
	}
	if p.Ping != 0 {
		out = append(out, pbInt(4, p.Ping)...)
	}
	if p.ID != 0 {
		out = append(out, pbInt(5, p.ID)...)
	}
	return out
}

// ServerData is the per-server payload; JSON tags mirror the protobufjs
// camelCase naming used by the /api/servers/single/ JSON endpoint.
type ServerData struct {
	SVMaxclients        int64             `json:"svMaxclients"`
	Clients             int64             `json:"clients"`
	Protocol            int64             `json:"protocol,omitempty"`
	Hostname            string            `json:"hostname"`
	Gametype            string            `json:"gametype"`
	Mapname             string            `json:"mapname"`
	Resources           []string          `json:"resources"`
	Server              string            `json:"server"`
	IconVersion         int64             `json:"iconVersion,omitempty"`
	Players             []Player          `json:"players,omitempty"`
	Vars                map[string]string `json:"vars"`
	EnhancedHostSupport bool              `json:"enhancedHostSupport"`
	UpvotePower         int64             `json:"upvotePower"`
	ConnectEndPoints    []string          `json:"connectEndPoints"`
	BurstPower          int64             `json:"burstPower"`
}

func encodeServerData(d *ServerData) []byte {
	var out []byte
	out = append(out, pbInt(1, d.SVMaxclients)...)
	out = append(out, pbInt(2, d.Clients)...)
	out = append(out, pbInt(3, d.Protocol)...)
	if d.Hostname != "" {
		out = append(out, pbString(4, d.Hostname)...)
	}
	if d.Gametype != "" {
		out = append(out, pbString(5, d.Gametype)...)
	}
	if d.Mapname != "" {
		out = append(out, pbString(6, d.Mapname)...)
	}
	for _, r := range d.Resources {
		out = append(out, pbString(8, r)...)
	}
	if d.Server != "" {
		out = append(out, pbString(9, d.Server)...)
	}
	for i := range d.Players {
		out = append(out, pbMessage(10, encodePlayer(&d.Players[i]))...)
	}
	if d.IconVersion != 0 {
		out = append(out, pbInt(11, d.IconVersion)...)
	}
	keys := make([]string, 0, len(d.Vars))
	for k := range d.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry := append(pbString(1, k), pbString(2, d.Vars[k])...)
		out = append(out, pbMessage(12, entry)...)
	}
	if d.EnhancedHostSupport {
		out = append(out, append(pbTag(16, 0), 1)...)
	}
	out = append(out, pbInt(17, d.UpvotePower)...)
	for _, c := range d.ConnectEndPoints {
		out = append(out, pbString(18, c)...)
	}
	out = append(out, pbInt(19, d.BurstPower)...)
	return out
}

func encodeServer(endPoint string, d *ServerData) []byte {
	var out []byte
	out = append(out, pbString(1, endPoint)...)
	out = append(out, pbMessage(2, encodeServerData(d))...)
	return out
}

func streamBytes(entries map[string]*entry) []byte {
	eps := make([]string, 0, len(entries))
	for ep := range entries {
		eps = append(eps, ep)
	}
	sort.Strings(eps)
	var out []byte
	var frame [4]byte
	for _, ep := range eps {
		d := entries[ep].data
		d.ConnectEndPoints = []string{ep}
		payload := encodeServer(ep, &d)
		binary.LittleEndian.PutUint32(frame[:], uint32(len(payload)))
		out = append(out, frame[:]...)
		out = append(out, payload...)
	}
	return out
}

// ---------------------------------------------------------------------------
// registry
// ---------------------------------------------------------------------------

type entry struct {
	data    ServerData
	last    time.Time
	keyID   string
	ownerID int64
}

type registry struct {
	mu      sync.RWMutex
	servers map[string]*entry
}

func newRegistry() *registry {
	return &registry{servers: map[string]*entry{}}
}

func (r *registry) upsert(endPoint string, data ServerData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[endPoint] = &entry{data: data, last: time.Now()}
}

func (r *registry) upsertWithOwner(endPoint string, data ServerData, keyID string, ownerID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.servers[endPoint]; ok {
		if time.Since(existing.last) <= heartbeatTTL && existing.keyID != "" && isServerKeyActive(existing.keyID) {
			if existing.ownerID != 0 && existing.ownerID != ownerID {
				return false
			}
		}
	}
	r.servers[endPoint] = &entry{data: data, last: time.Now(), keyID: keyID, ownerID: ownerID}
	return true
}

func (r *registry) evictByKey(keyID string) int {
	if keyID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	evicted := 0
	for ep, e := range r.servers {
		if e.keyID == keyID {
			delete(r.servers, ep)
			evicted++
		}
	}
	return evicted
}

func (r *registry) get(endPoint string) *ServerData {
	r.mu.RLock()
	e, ok := r.servers[endPoint]
	r.mu.RUnlock()
	if !ok || time.Since(e.last) > heartbeatTTL || (e.keyID != "" && !isServerKeyActive(e.keyID)) {
		return nil
	}
	d := e.data
	d.ConnectEndPoints = []string{endPoint}
	return &d
}

func (r *registry) live() map[string]*entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ep, e := range r.servers {
		if time.Since(e.last) > heartbeatTTL || (e.keyID != "" && !isServerKeyActive(e.keyID)) {
			delete(r.servers, ep)
		}
	}
	out := make(map[string]*entry, len(r.servers))
	for ep, e := range r.servers {
		out[ep] = e
	}
	return out
}

var reg = newRegistry()

// ---------------------------------------------------------------------------
// owner mods (upvotes / fake players) + feed, persisted to state.json
// ---------------------------------------------------------------------------

type serverMods struct {
	Upvotes       *int64 `json:"upvotes,omitempty"`     // absolute override
	FakePlayerCnt int64  `json:"fakePlayers,omitempty"` // 1..9000, 0 = off
}

type feedMedia struct {
	URL string `json:"url"`
	W   int    `json:"w"`
	H   int    `json:"h"`
}

type feedItem struct {
	ID         string      `json:"id"`
	Text       string      `json:"text"`
	Author     string      `json:"author"`
	ScreenName string      `json:"screenName"`
	Avatar     string      `json:"avatar,omitempty"`
	URL        string      `json:"url,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
	Source     string      `json:"source"`
	Media      []feedMedia `json:"media,omitempty"`
}

type ownerState struct {
	Servers map[string]serverMods `json:"servers"`
	Feed    []feedItem            `json:"feed"`
	FeedIDs map[string]bool       `json:"-"` // dedupe set (rebuilt on load)
}

var (
	stateMu  sync.RWMutex
	state    = ownerState{Servers: map[string]serverMods{}, FeedIDs: map[string]bool{}}
	ownerTok string
)

func statePath() string { return *stateFile }

func loadState() {
	b, err := os.ReadFile(statePath())
	if err != nil {
		return
	}
	var s ownerState
	if json.Unmarshal(b, &s) != nil {
		log.Printf("state.json unreadable, starting fresh")
		return
	}
	if s.Servers == nil {
		s.Servers = map[string]serverMods{}
	}
	s.FeedIDs = map[string]bool{}
	for _, it := range s.Feed {
		s.FeedIDs[it.ID] = true
	}
	stateMu.Lock()
	state = s
	stateMu.Unlock()
	log.Printf("state loaded: %d server mods, %d feed items", len(s.Servers), len(s.Feed))
}

func saveStateLocked() {
	b, err := json.MarshalIndent(&state, "", " ")
	if err != nil {
		return
	}
	tmp := statePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, statePath())
	}
}

// applyMods overlays owner settings onto live server data (caller holds copy).
func applyMods(ep string, d *ServerData) {
	stateMu.RLock()
	m, ok := state.Servers[ep]
	stateMu.RUnlock()
	if !ok {
		return
	}
	if m.Upvotes != nil {
		d.UpvotePower = *m.Upvotes
	}
	// Local-first: never fabricate players or counts from persisted admin state.

}

// ---------------------------------------------------------------------------
// heartbeat -> ServerData mapping
// ---------------------------------------------------------------------------

type heartbeat struct {
	Port         int64  `json:"port"`
	ListingToken string `json:"listingToken"`
	IPOverride   string `json:"ipOverride"`
	Private      bool   `json:"private"`
	FallbackData struct {
		Players json.RawMessage        `json:"players"`
		Info    map[string]interface{} `json:"info"`
		Dynamic map[string]interface{} `json:"dynamic"`
	} `json:"fallbackData"`
}

func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func asInt(v interface{}, def int64) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n
		}
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
	}
	return def
}

func asStringSlice(v interface{}) []string {
	if arr, ok := v.([]interface{}); ok {
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			out = append(out, asString(e))
		}
		return out
	}
	return nil
}

func asStringMap(v interface{}) map[string]string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, e := range m {
		out[k] = asString(e)
	}
	return out
}

func serverDataFromHeartbeat(hb *heartbeat) ServerData {
	info := unwrapData(hb.FallbackData.Info, "info")
	dyn := unwrapData(hb.FallbackData.Dynamic, "dynamic")

	vars := map[string]string{}
	for _, m := range []map[string]string{asStringMap(info["vars"]), asStringMap(dyn["vars"])} {
		for k, v := range m {
			lk := strings.ToLower(strings.TrimSpace(k))
			if lk == "sv_nsmserverkey" || lk == "sv_licensekey" || lk == "sv_licensekeytoken" || lk == "authorization" {
				continue
			}
			vars[k] = redactServerKeys(v)
		}
	}
	vars["gamename"] = gamename // client browser filter requires this

	var clients int64
	if c, ok := dyn["clients"]; ok {
		clients = asInt(c, 0)
	} else if len(hb.FallbackData.Players) > 0 && hb.FallbackData.Players[0] == '[' {
		var arr []interface{}
		if json.Unmarshal(hb.FallbackData.Players, &arr) == nil {
			clients = int64(len(arr))
		}
	}

	hostname := redactServerKeys(asString(dyn["hostname"]))
	if hostname == "" {
		hostname = redactServerKeys(asString(info["hostname"]))
	}
	if hostname == "" {
		hostname = "NSM Server"
	}

	return ServerData{
		SVMaxclients:        asInt(dyn["svMaxclients"], 48),
		Clients:             clients,
		Hostname:            hostname,
		Gametype:            redactServerKeys(asString(dyn["gametype"])),
		Mapname:             redactServerKeys(asString(dyn["mapname"])),
		Resources:           append([]string{}, asStringSlice(info["resources"])...),
		Server:              redactServerKeys(asString(info["version"])),
		Vars:                vars,
		EnhancedHostSupport: info["enhancedHostSupport"] == true || asInt(info["enhancedHostSupport"], 0) != 0,
		UpvotePower:         0,
		BurstPower:          0,
	}
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// Ticket public keys are explicitly configured; the backend never issues tickets.
type handler struct{}

func setCors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization, User-Api-Key, User-Api-Client-Id, X-CSRF-Token, X-Requested-With, Discourse-Present, Discourse-Logged-In, Discourse-Visible, Ownership-Ticket, Cfx-Entitlement-Ticket, X-NSM-Session, *")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
}

func sendJSON(w http.ResponseWriter, code int, v interface{}) {
	body, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	setCors(w)
	w.WriteHeader(code)
	w.Write(body)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isOwner(r *http.Request) bool {
	if ownerTok == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	ok := auth == "Bearer "+ownerTok
	if !ok {
		time.Sleep(500 * time.Millisecond) // blunt brute force
	}
	return ok
}

// twitterTime renders times the way the UI's tweet parser expects.
func twitterTime(t time.Time) string { return t.Format("Mon Jan 02 15:04:05 -0700 2006") }

// tweetShape converts a feed item into the raw Twitter-format object the
// client's rawTweetToActivityDataItem() consumes.
func tweetShape(it feedItem) map[string]interface{} {
	user := map[string]interface{}{
		"name":        it.Author,
		"screen_name": it.ScreenName,
	}
	if it.Avatar != "" {
		user["profile_image_url_https"] = it.Avatar
	}
	tw := map[string]interface{}{
		"id_str":     it.ID,
		"text":       it.Text,
		"created_at": twitterTime(it.CreatedAt),
		"user":       user,
		"entities":   map[string]interface{}{"urls": []interface{}{}},
	}
	if len(it.Media) > 0 {
		media := make([]interface{}, 0, len(it.Media))
		for _, m := range it.Media {
			w, h := m.W, m.H
			if w <= 0 || h <= 0 {
				w, h = 1280, 720
			}
			media = append(media, map[string]interface{}{
				"id_str":          it.ID + "_m",
				"type":            "photo",
				"media_url":       m.URL,
				"media_url_https": m.URL,
				"sizes":           map[string]interface{}{"large": map[string]int{"w": w, "h": h}},
			})
		}
		tw["extended_entities"] = map[string]interface{}{"media": media}
	}
	return tw
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setCors(w)
	w.Header().Set("X-NSM-Backend", backendVersion)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := r.URL.Path
	if path == "/api/serverlist/ingress" && r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		sendJSON(w, 405, map[string]string{"lastError": "POST required"})
		return
	}
	if path != "/api/serverlist/ingress" && !strings.HasPrefix(path, "/admin/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
		if path != "/api/ticket/identities" && path != "/api/identities" && path != "/api/upvote/" {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			sendJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
	}

	// ---- owner admin API ----
	if strings.HasPrefix(path, "/admin/") {
		if !isOwner(r) {
			sendJSON(w, http.StatusUnauthorized, map[string]string{"error": "owner token required"})
			return
		}
		h.serveAdmin(w, r, path)
		return
	}

	switch {
	case path == "/health":
		sendJSON(w, 200, map[string]interface{}{"service": "nsm-local-first", "version": backendVersion,
			"pid": os.Getpid(), "auth": "external-validation-required", "telemetry": "disabled",
			"heartbeatTTLSeconds": 300, "servers": len(reg.live()), "routes": uiRoutes})
	case path == "/":
		body := "NSM masterlist service v2\n"
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)

	case path == "/api/servers/streamRedir/" || path == "/api/servers/streamRedir":
		live := reg.live()
		out := make(map[string]*entry, len(live))
		for ep, e := range live {
			d := e.data
			applyMods(ep, &d)
			out[ep] = &entry{data: d, last: e.last}
		}
		body := streamBytes(out)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write(body)

	case strings.HasPrefix(path, "/api/servers/single/"):
		ep := strings.TrimPrefix(path, "/api/servers/single/")
		if d := reg.get(ep); d != nil {
			applyMods(ep, d)
			sendJSON(w, http.StatusOK, map[string]interface{}{"EndPoint": ep, "Data": d})
		} else {
			sendJSON(w, http.StatusNotFound, map[string]interface{}{})
		}

	case strings.HasPrefix(path, "/api/servers/icon/"):
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusNotFound)

	case path == "/api/ticket/pubkey":
		if ticketPublicKey == "" {
			sendJSON(w, 503, map[string]string{"error": "ticket authentication unavailable: no trusted public key configured"})
		} else {
			sendJSON(w, 200, map[string]string{"key": ticketPublicKey})
		}

	case path == "/api/identities" || path == "/api/ticket/identities":
		if r.Method == http.MethodPost && path == "/api/identities" {
			sendJSON(w, 503, map[string]string{"error": "identity linking is not available in local mode"})
		} else if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodPost && path == "/api/ticket/identities" {
			sendJSON(w, 200, map[string]interface{}{"identities": []interface{}{}})
		} else {
			sendJSON(w, 405, map[string]string{"error": "method not allowed"})
		}
	case path == "/api/upvote/":
		sendJSON(w, 403, map[string]string{"error": "authenticated boost service unavailable in local mode"})
	case path == "/v1/public/featured-servers":
		sendJSON(w, http.StatusOK, map[string]interface{}{"pinnedServers": []string{}, "pinIfEmpty": false})

	case path == "/v1/public/notices/fivem":
		sendJSON(w, http.StatusOK, []interface{}{})

	case path == "/v1/public/tweets":
		stateMu.RLock()
		items := make([]feedItem, len(state.Feed))
		copy(items, state.Feed)
		stateMu.RUnlock()
		// newest first, highest id first
		sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
		out := make([]interface{}, 0, len(items))
		for _, it := range items {
			out = append(out, tweetShape(it))
		}
		sendJSON(w, http.StatusOK, out)

	case strings.HasPrefix(path, "/v1/public/pool-size-limits/"):
		sendJSON(w, http.StatusOK, map[string]int{})

	case path == "/runtime/counts.json":
		players := int64(0)
		for ep, e := range reg.live() {
			d := e.data
			applyMods(ep, &d)
			players += d.Clients
		}
		sendJSON(w, http.StatusOK, []interface{}{players, nil, nil})

	case path == "/api/serverlist/ingress" && r.Method == http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		var hb *heartbeat
		if err := dec.Decode(&hb); err != nil || hb == nil {
			var max *http.MaxBytesError
			code := 400
			if errors.As(err, &max) {
				code = 413
			}
			sendJSON(w, code, map[string]string{"lastError": "invalid heartbeat JSON"})
			return
		}
		var extra interface{}
		if dec.Decode(&extra) != io.EOF {
			sendJSON(w, 400, map[string]string{"lastError": "unexpected trailing JSON"})
			return
		}
		// GameServer.cpp marks heartbeats to a custom master as "private".
		// This loopback-only service is that explicitly selected local master;
		// accept the record locally, never publish it to an upstream master.
		if err := validateHeartbeat(hb); err != nil {
			sendJSON(w, 400, map[string]string{"lastError": err.Error()})
			return
		}
		ip := clientIP(r)
		if override := strings.TrimSpace(hb.IPOverride); override != "" {
			// A loopback-only deployment may advertise another loopback address, not spoof a remote server.
			if net.ParseIP(override) == nil || !net.ParseIP(override).IsLoopback() {
				sendJSON(w, 400, map[string]string{"lastError": "ipOverride must be a loopback IP"})
				return
			}
			ip = override
		}
		port := hb.Port
		if port == 0 {
			port = 30120
		}
		endPoint := net.JoinHostPort(ip, strconv.FormatInt(port, 10))
		rawKey := extractHeartbeatServerKey(r, hb)
		var keyID string
		var ownerID int64
		if rawKey != "" || requireKeys {
			keyRec, code, reason := validateServerKey(rawKey, endPoint)
			if code != http.StatusOK || keyRec == nil {
				sendJSON(w, code, map[string]string{"lastError": reason})
				return
			}
			keyID = keyRec.ID
			ownerID = keyRec.UserID
		}
		data := serverDataFromHeartbeat(hb)
		if !reg.upsertWithOwner(endPoint, data, keyID, ownerID) {
			sendJSON(w, http.StatusForbidden, map[string]string{"lastError": "endpoint_owned_by_another_account"})
			return
		}
		log.Printf("[heartbeat] %s clients=%d", endPoint, data.Clients)
		sendJSON(w, 200, map[string]interface{}{})

	default:
		sendJSON(w, http.StatusNotFound, map[string]string{"error": "not found", "path": path})
	}
}

func (h *handler) serveAdmin(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost && path != "/admin/state" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	switch path {

	case "/admin/state":
		stateMu.RLock()
		snap := ownerState{Servers: state.Servers, Feed: state.Feed}
		stateMu.RUnlock()
		sendJSON(w, http.StatusOK, snap)

	case "/admin/upvotes":
		var req struct {
			Endpoints []string `json:"endpoints"`
			Set       *int64   `json:"set"`
			Add       *int64   `json:"add"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Endpoints) == 0 ||
			(req.Set == nil && req.Add == nil) {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": "need endpoints and set or add"})
			return
		}
		stateMu.Lock()
		updated := map[string]int64{}
		for _, ep := range req.Endpoints {
			m := state.Servers[ep]
			switch {
			case req.Set != nil:
				v := *req.Set
				m.Upvotes = &v
			case req.Add != nil && m.Upvotes != nil:
				v := *m.Upvotes + *req.Add
				if v < 0 {
					v = 0
				}
				m.Upvotes = &v
			case req.Add != nil:
				v := *req.Add
				if v < 0 {
					v = 0
				}
				m.Upvotes = &v
			}
			state.Servers[ep] = m
			updated[ep] = *m.Upvotes
		}
		saveStateLocked()
		stateMu.Unlock()
		log.Printf("[admin] upvotes: %v", updated)
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "upvotes": updated})

	case "/admin/fakeplayers":
		sendJSON(w, 410, map[string]string{"error": "fabricated player counts are disabled"})
		return

	case "/admin/feed":
		var req struct {
			Text       string `json:"text"`
			Author     string `json:"author"`
			ScreenName string `json:"screenName"`
			Avatar     string `json:"avatar"`
			URL        string `json:"url"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Text) == "" {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": "need text"})
			return
		}
		if req.Author == "" {
			req.Author = "NSM"
		}
		if req.ScreenName == "" {
			req.ScreenName = "NSMCommunity"
		}
		stateMu.Lock()
		id := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
		state.Feed = append(state.Feed, feedItem{
			ID: id, Text: req.Text, Author: req.Author, ScreenName: req.ScreenName,
			Avatar: req.Avatar, URL: req.URL, CreatedAt: time.Now(), Source: "owner",
		})
		state.FeedIDs[id] = true
		saveStateLocked()
		stateMu.Unlock()
		log.Printf("[admin] feed post %s", id)
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": id})

	case "/admin/feed/import":
		var req struct {
			Items []struct {
				ID         string      `json:"id"`
				Text       string      `json:"text"`
				Author     string      `json:"author"`
				ScreenName string      `json:"screenName"`
				Avatar     string      `json:"avatar"`
				URL        string      `json:"url"`
				CreatedAt  string      `json:"createdAt"` // RFC3339
				Source     string      `json:"source"`
				Media      []feedMedia `json:"media"`
			} `json:"items"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		added, skipped := 0, 0
		stateMu.Lock()
		for _, it := range req.Items {
			if it.ID == "" || it.Text == "" || state.FeedIDs[it.ID] {
				skipped++
				continue
			}
			ca := time.Now()
			if it.CreatedAt != "" {
				if t, err := time.Parse(time.RFC3339, it.CreatedAt); err == nil {
					ca = t
				}
			}
			src := it.Source
			if src == "" {
				src = "discord"
			}
			state.Feed = append(state.Feed, feedItem{
				ID: it.ID, Text: it.Text, Author: it.Author, ScreenName: it.ScreenName,
				Avatar: it.Avatar, URL: it.URL, CreatedAt: ca, Source: src, Media: it.Media,
			})
			state.FeedIDs[it.ID] = true
			added++
		}
		// keep newest 200
		if len(state.Feed) > 200 {
			sort.Slice(state.Feed, func(i, j int) bool { return state.Feed[i].ID > state.Feed[j].ID })
			state.Feed = state.Feed[:200]
		}
		saveStateLocked()
		stateMu.Unlock()
		log.Printf("[admin] feed import: %d added, %d skipped", added, skipped)
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "added": added, "skipped": skipped})

	case "/admin/feed/delete":
		var req struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ID == "" {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": "need id"})
			return
		}
		stateMu.Lock()
		out := state.Feed[:0]
		deleted := false
		for _, it := range state.Feed {
			if it.ID == req.ID {
				deleted = true
				delete(state.FeedIDs, it.ID)
				continue
			}
			out = append(out, it)
		}
		state.Feed = out
		saveStateLocked()
		stateMu.Unlock()
		if !deleted {
			sendJSON(w, http.StatusNotFound, map[string]string{"error": "no such id"})
			return
		}
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true})

	default:
		sendJSON(w, http.StatusNotFound, map[string]string{"error": "unknown admin path"})
	}
}

var stateFile = flag.String("state", "state.json", "path to persistent state file")

var ownerName = "Cravenz"

func stopPreviousMasterlistInstances(addr string, port int) {
	pid := os.Getpid()
	// 1. Politely ask any running NSM-Masterlist on this address to exit
	cl := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := cl.Post("http://"+addr+"/api/nsm/internal-restart", "application/json", strings.NewReader(`{}`))
	if err == nil && resp != nil {
		resp.Body.Close()
	}

	if runtime.GOOS != "windows" {
		return
	}

	// 2. Kill any previous NSM-Masterlist.exe (or NSM-Masterlist (1).exe) process except our own PID
	_ = exec.Command("taskkill", "/F", "/IM", "NSM-Masterlist.exe", "/FI", fmt.Sprintf("PID ne %d", pid)).Run()
	if exe, err := os.Executable(); err == nil {
		base := filepath.Base(exe)
		if base != "" && !strings.EqualFold(base, "NSM-Masterlist.exe") {
			_ = exec.Command("taskkill", "/F", "/IM", base, "/FI", fmt.Sprintf("PID ne %d", pid)).Run()
		}
	}
	psCmd := fmt.Sprintf("Get-Process | Where-Object { ($_.ProcessName -like 'NSM-Masterlist*' -or $_.ProcessName -like 'nsm-masterlist*') -and $_.Id -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue", pid)
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", psCmd).Run()

	// 3. Also check netstat for any user process holding 127.0.0.1:<port>
	if out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output(); err == nil {
		targetSuffix := ":" + strconv.Itoa(port)
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 5 && strings.EqualFold(fields[0], "TCP") && strings.EqualFold(fields[3], "LISTENING") {
				localAddr := fields[1]
				if strings.HasSuffix(localAddr, targetSuffix) {
					otherPID, err := strconv.Atoi(fields[4])
					if err == nil && otherPID > 4 && otherPID != pid {
						_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(otherPID)).Run()
					}
				}
			}
		}
	}
}

func pauseBeforeExitOnWindows(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Println(msg)
	if runtime.GOOS == "windows" {
		fmt.Println("\n------------------------------------------------------------")
		fmt.Println("Press Enter to close this window...")
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
	}
	os.Exit(1)
}

func main() {
	// Ensure working directory is the executable's directory when launched by double-click on Windows
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		base := strings.ToLower(filepath.Base(exe))
		if exeDir != "" && !strings.Contains(base, ".test") {
			_ = os.Chdir(exeDir)
		}
	}

	bind := flag.String("bind", "127.0.0.1", "loopback address for local-only service")
	port := flag.Int("port", 80, "port to listen on")
	owner := flag.String("owner-token", os.Getenv("OWNER_TOKEN"), "owner admin API bearer token")
	oname := flag.String("owner-name", os.Getenv("OWNER_NAME"), "username that holds the owner role")
	opass := flag.String("owner-pass", os.Getenv("OWNER_PASS"), "set a new password for the owner account at startup")
	keyPath := flag.String("ticket-public-key", "", "optional trusted RSA public key PEM; no tickets are issued")
	reqKeys := flag.Bool("require-keys", os.Getenv("NSM_REQUIRE_KEYS") == "1", "require a valid NSM keymaster server key on /api/serverlist/ingress")
	flag.Parse()
	requireKeys = *reqKeys
	if ip := net.ParseIP(*bind); ip == nil || !ip.IsLoopback() {
		pauseBeforeExitOnWindows("local-first backend requires a loopback bind address")
	}
	if *port < 1 || *port > 65535 {
		pauseBeforeExitOnWindows("invalid HTTP port")
	}
	if *keyPath != "" {
		var err error
		ticketPublicKey, err = readTicketPublicKey(*keyPath)
		if err != nil {
			pauseBeforeExitOnWindows("ticket public key error: %v", err)
		}
	}

	if *oname != "" {
		ownerName = *oname
	}

	ownerTok = *owner
	if ownerTok == "" {
		log.Printf("NOTE: OWNER_TOKEN not set - bearer-token admin API is disabled (website owner login still works)")
	}

	loadState()
	loadAccounts()
	if *opass != "" {
		setOwnerPassword(*opass)
	}
	loadForum()
	initSite()
	patchLocalUIBundlesOnDisk()

	addr := net.JoinHostPort(*bind, strconv.Itoa(*port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("Port %s is currently in use — stopping previous NSM-Masterlist instance...", addr)
		stopPreviousMasterlistInstances(addr, *port)
		for attempt := 0; attempt < 20; attempt++ {
			time.Sleep(250 * time.Millisecond)
			ln, err = net.Listen("tcp", addr)
			if err == nil {
				log.Printf("Successfully took over %s from previous instance.", addr)
				break
			}
		}
	}
	if err != nil {
		pauseBeforeExitOnWindows("Could not bind http://%s: %v\nTip: Try right-clicking NSM-Masterlist.exe and choosing 'Run as administrator'.", addr, err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           localHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("================================================================")
	log.Printf("NSM Masterlist + 1:1 Website + AFX Forum + Accounts (v0.7.8-f)")
	log.Printf("Listening on: http://%s", addr)
	log.Printf("  Homepage:    http://%s/", addr)
	log.Printf("  Server List: http://%s/servers", addr)
	log.Printf("  AFX Forum:   http://%s/forum", addr)
	log.Printf("  Your Profile:http://%s/u/Cravenz", addr)
	log.Printf("  Edit Socials:http://%s/u/Cravenz?tab=preferences", addr)
	log.Printf("================================================================")
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		pauseBeforeExitOnWindows("listen error: %v", err)
	}
}
