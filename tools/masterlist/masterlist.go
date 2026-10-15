// NSM masterlist service v2
// =========================
// Standalone replacement for the Cfx masterlist endpoints used by NSM
// clients (v3+) and servers (v6+). Single file, standard library only.
//
// Build:
//   go build -o nsm-masterlist .
//   GOOS=windows GOARCH=amd64 go build -o NSM-Masterlist.exe .
//   GOOS=linux GOARCH=amd64 go build -o nsm-masterlist-linux-amd64 .
//   GOOS=linux GOARCH=arm64 go build -o nsm-masterlist-linux-arm64 .
//
// Endpoints (all on one port, default 80):
//   GET  /api/servers/streamRedir/     full list: [4-byte LE length][protobuf master.Server] frames
//   GET  /api/servers/single/<ip:port> single server JSON {EndPoint, Data}
//   GET  /api/servers/icon/...         server icon (404 -> client uses default)
//   POST /api/serverlist/ingress       server heartbeat (GameServer.cpp)
//   GET  /api/ticket/pubkey            RSA public key (unused with sv_lan true)
//   GET  /api/identities               player identities (empty)
//   GET  /api/ticket/identities        ticket identities (empty)
//   GET  /v1/public/featured-servers   featured servers (empty)
//   GET  /v1/public/notices/fivem      notices (empty)
//   GET  /v1/public/tweets             official feed (Twitter-format JSON array;
//                                      sources: owner posts + Discord poller)
//   GET  /v1/public/pool-size-limits/* pool limits (empty = unlimited)
//   GET  /runtime/counts.json          player counts [n, 0, n]
//
// Owner admin API (Authorization: Bearer $OWNER_TOKEN, owner-only):
//   POST /admin/upvotes      {"endpoints":[...], "set":N} or {"add":N}
//   POST /admin/fakeplayers  {"endpoints":[...], "count":N}  (0..9000, 0=off)
//   POST /admin/feed         {"text":"...", "author":"...", "screenName":"...",
//                             "avatar":"...", "url":"..."}   (owner post)
//   POST /admin/feed/import  {"items":[{id,text,author,screenName,avatar,url,
//                             createdAt,media:[{url,w,h}]}]}  (poller, id-deduped)
//   POST /admin/feed/delete  {"id":"..."}
//   GET  /admin/state        current mods + feed
//
// Entries expire 5 minutes after their last heartbeat. Owner mods (upvotes,
// fake players) persist in state.json and survive restarts.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	heartbeatTTL = 300 * time.Second
	gamename     = "gta5"
	fakeMax      = 9000
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
	data ServerData
	last time.Time
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

func (r *registry) get(endPoint string) *ServerData {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.servers[endPoint]
	if !ok || time.Since(e.last) > heartbeatTTL {
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
		if time.Since(e.last) > heartbeatTTL {
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
	Upvotes        *int64 `json:"upvotes,omitempty"`        // absolute override
	FakePlayerCnt  int64  `json:"fakePlayers,omitempty"`    // 1..9000, 0 = off
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
	stateMu sync.RWMutex
	state   = ownerState{Servers: map[string]serverMods{}, FeedIDs: map[string]bool{}}
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
	if m.FakePlayerCnt > 0 {
		fake := m.FakePlayerCnt
		if fake > fakeMax {
			fake = fakeMax
		}
		if fake > d.Clients {
			d.Clients = fake
			d.Players = fakePlayers(ep, int(fake))
		}
	}
}

// ---------------------------------------------------------------------------
// fake player names: deterministic per server (stable across refreshes)
// ---------------------------------------------------------------------------

var fakeAdj = []string{"Silent", "Rapid", "Wild", "Lunar", "Steel", "Neon", "Shadow", "Crimson", "Frost", "Turbo",
	"Savage", "Golden", "Electric", "Midnight", "Rogue", "Blaze", "Venom", "Iron", "Cosmic", "Ghost"}
var fakeNoun = []string{"Wolf", "Tiger", "Raptor", "Viper", "Falcon", "Reaper", "Titan", "Phantom", "Drifter", "Bandit",
	"Ranger", "Hunter", "Sniper", "Panda", "Cobra", "Panther", "Dragon", "Hawk", "Fox", "Bear"}

func fakePlayers(ep string, n int) []Player {
	// stable seed from endpoint
	var seed uint64 = 1469598103934665603
	for i := 0; i < len(ep); i++ {
		seed ^= uint64(ep[i])
		seed *= 1099511628211
	}
	start := int(seed % 400) // rotate base combos per server
	out := make([]Player, 0, n)
	for i := 0; i < n; i++ {
		// step 37 (coprime with 400) so consecutive names vary both parts
		idx := (start + i*37) % 400
		name := fakeAdj[idx%20] + fakeNoun[(idx/20)%20]
		if s := i / 400; s > 0 {
			name += fmt.Sprintf("%02d", s%100)
		}
		ping := int64(20 + (seed>>uint(i%32))%140) // 20..160ms, deterministic
		out = append(out, Player{Name: name, Ping: ping, ID: int64(i + 1)})
	}
	return out
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
	info := hb.FallbackData.Info
	dyn := hb.FallbackData.Dynamic

	vars := asStringMap(info["vars"])
	for k, v := range asStringMap(dyn["vars"]) {
		vars[k] = v
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

	hostname := asString(dyn["hostname"])
	if hostname == "" {
		hostname = asString(info["hostname"])
	}
	if hostname == "" {
		hostname = "NSM Server"
	}

	return ServerData{
		SVMaxclients:        asInt(dyn["svMaxclients"], 48),
		Clients:             clients,
		Hostname:            hostname,
		Gametype:            asString(dyn["gametype"]),
		Mapname:             asString(dyn["mapname"]),
		Resources:           asStringSlice(info["resources"]),
		Server:              asString(info["version"]),
		Vars:                vars,
		EnhancedHostSupport: asInt(info["enhancedHostSupport"], 0) != 0,
		UpvotePower:         0,
		BurstPower:          0,
	}
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// Public key served at /api/ticket/pubkey (PKCS#1 RSAPublicKey, base64 DER).
// Matches tools/masterlist/ticket-signing-key.pem. Only used when a server
// runs with sv_lan false; NSM servers should set `sv_lan "true"` so connect
// tickets are not required at all.
const ticketPubkeyB64 = "MIIBCgKCAQEAwleR7LYjK6zv5GPU4+go+4u42JI8fA/hX4wykkwhxG8fLpdGTA1kjQm+NWrWi7Xg" +
	"OMOAVzeiMCpr5cLlcTabf1fJpexyN1XKmQLvnIxVgofc/N/ZvzVbnqTV5RGJ7ZfnXDd//r+bBuxg" +
	"DWJIEAiXnYAwTSZq5crxgXorya7gVTcD2P/RQQUCCzuniFaXO2mROxj28UDSihVCKriTd1p6StxX" +
	"SnsnekH6av5uuCENf1KHL85n7bl4gKZQoG0qoYQxohigjfUXvvO93AbN7M10r12rvgLcLTCHhMci" +
	"y0xEmbj2pI5b9wOjPbx/oHqn5rtAhqYae4803I80aZRweafWzQIDAQAB"

type handler struct{}

func setCors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
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
	if ff := r.Header.Get("X-Forwarded-For"); ff != "" {
		return strings.TrimSpace(strings.Split(ff, ",")[0])
	}
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
		"name":         it.Author,
		"screen_name":  it.ScreenName,
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
				"id_str":           it.ID + "_m",
				"type":             "photo",
				"media_url":        m.URL,
				"media_url_https":  m.URL,
				"sizes":            map[string]interface{}{"large": map[string]int{"w": w, "h": h}},
			})
		}
		tw["extended_entities"] = map[string]interface{}{"media": media}
	}
	return tw
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setCors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := r.URL.Path

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
	case path == "/" || path == "/health":
		body := "NSM masterlist service v2\n"
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)

	case path == "/api/servers/streamRedir/":
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
		sendJSON(w, http.StatusOK, map[string]string{"key": ticketPubkeyB64})

	case path == "/api/identities" || path == "/api/ticket/identities":
		sendJSON(w, http.StatusOK, []string{})

	case path == "/v1/public/featured-servers":
		sendJSON(w, http.StatusOK, []string{})

	case path == "/v1/public/notices/fivem":
		sendJSON(w, http.StatusOK, map[string][]string{"notices": {}})

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
		sendJSON(w, http.StatusOK, []int64{players, 0, players})

	case path == "/api/serverlist/ingress" && r.Method == http.MethodPost:
		var hb heartbeat
		if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
			sendJSON(w, http.StatusBadRequest, map[string]string{"lastError": "bad json: " + err.Error()})
			return
		}
		ip := strings.TrimSpace(hb.IPOverride)
		if ip == "" {
			ip = clientIP(r)
		}
		port := hb.Port
		if port == 0 {
			port = 30120
		}
		endPoint := net.JoinHostPort(ip, strconv.FormatInt(port, 10))
		// JoinHostPort adds brackets for IPv6; the masterlist format is plain ip:port
		endPoint = strings.ReplaceAll(endPoint, "[", "")
		endPoint = strings.ReplaceAll(endPoint, "]", "")
		reg.upsert(endPoint, serverDataFromHeartbeat(&hb))
		d := reg.get(endPoint)
		clients := int64(0)
		if d != nil {
			clients = d.Clients
		}
		log.Printf("[heartbeat] %s clients=%d", endPoint, clients)
		sendJSON(w, http.StatusOK, map[string]interface{}{}) // empty lastError = success

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
		var req struct {
			Endpoints []string `json:"endpoints"`
			Count     *int64   `json:"count"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Endpoints) == 0 || req.Count == nil {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": "need endpoints and count (0..9000)"})
			return
		}
		n := *req.Count
		if n < 0 {
			n = 0
		}
		if n > fakeMax {
			n = fakeMax
		}
		stateMu.Lock()
		updated := map[string]int64{}
		for _, ep := range req.Endpoints {
			m := state.Servers[ep]
			m.FakePlayerCnt = n
			state.Servers[ep] = m
			updated[ep] = n
		}
		saveStateLocked()
		stateMu.Unlock()
		log.Printf("[admin] fakeplayers: %v", updated)
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "fakePlayers": updated})

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

func main() {
	bind := flag.String("bind", "0.0.0.0", "address to bind")
	port := flag.Int("port", 80, "port to listen on")
	owner := flag.String("owner-token", os.Getenv("OWNER_TOKEN"), "owner admin API bearer token")
	flag.Parse()

	ownerTok = *owner
	if ownerTok == "" {
		log.Printf("WARNING: OWNER_TOKEN not set - /admin/* endpoints are disabled")
	}

	loadState()

	addr := net.JoinHostPort(*bind, strconv.Itoa(*port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           &handler{},
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("NSM masterlist v2 listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen error: %v", err)
		os.Exit(1)
	}
}
