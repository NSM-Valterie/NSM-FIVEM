// NSM website: embedded pages served by the same exe.
// Templates in site/templates, static in site/static. Go 1.22 mux patterns.
package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"time"
)

//go:embed site
var siteFS embed.FS

var tmpl *template.Template

func initSite() {
	tmpl = template.Must(template.ParseFS(siteFS, "site/templates/*.tmpl"))
}

func renderPage(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = map[string]interface{}{}
	}
	if _, hasUser := data["User"]; !hasUser {
		data["User"] = currentUser(r)
	}
	if _, hasPage := data["Page"]; !hasPage {
		data["Page"] = r.URL.Path
	}
	data["Year"] = time.Now().Year()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

// liveServers returns the current masterlist registry entries for the site.
func liveServers() []map[string]interface{} {
	out := []map[string]interface{}{}
	for ep, e := range reg.live() {
		d := e.data
		applyMods(ep, &d)
		out = append(out, map[string]interface{}{
			"EndPoint": ep, "Hostname": d.Hostname, "Clients": d.Clients,
			"MaxClients": d.SVMaxclients, "GameType": d.Gametype, "MapName": d.Mapname,
			"Upvotes": upvoteCount(ep),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["Clients"].(int64) > out[j]["Clients"].(int64)
	})
	return out
}

func totalPlayers() int64 {
	var n int64
	for _, e := range reg.live() {
		d := e.data
		applyMods("", &d)
		n += d.Clients
	}
	return n
}

// ---- pages ----

func homePage(w http.ResponseWriter, r *http.Request) {
	stateMu.RLock()
	feedItems := make([]feedItem, len(state.Feed))
	copy(feedItems, state.Feed)
	stateMu.RUnlock()
	sort.Slice(feedItems, func(i, j int) bool { return feedItems[i].ID > feedItems[j].ID })
	if len(feedItems) > 5 {
		feedItems = feedItems[:5]
	}
	servers := liveServers()
	if len(servers) > 6 {
		servers = servers[:6]
	}
	accMu.Lock()
	accountCount := len(acc.Users)
	accMu.Unlock()
	renderPage(w, r, "index.tmpl", map[string]interface{}{
		"Title": "Home", "Feed": feedItems, "Servers": servers,
		"Stats": map[string]int64{"Players": totalPlayers(), "Servers": int64(len(liveServers())), "Accounts": int64(accountCount)},
	})
}

func serversPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "servers.tmpl", map[string]interface{}{"Title": "Server List", "Servers": liveServers()})
}

func serverDetailPage(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	d := reg.get(ep)
	if d == nil {
		renderPage(w, r, "server_missing.tmpl", map[string]interface{}{"Title": "Server", "EndPoint": ep})
		return
	}
	applyMods(ep, d)
	u := currentUser(r)
	voted := false
	if u != nil {
		accMu.Lock()
		for _, id := range acc.Upvotes[ep] {
			if id == u.ID {
				voted = true
			}
		}
		accMu.Unlock()
	}
	renderPage(w, r, "server.tmpl", map[string]interface{}{
		"Title": "Server", "EndPoint": ep, "Srv": d, "Upvotes": upvoteCount(ep), "Voted": voted,
	})
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "login.tmpl", map[string]interface{}{"Title": "Log in"})
}
func registerPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "register.tmpl", map[string]interface{}{"Title": "Create account"})
}
func logoutPage(w http.ResponseWriter, r *http.Request) {
	apiLogout(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func accountPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "account.tmpl", map[string]interface{}{"Title": "My account"})
}
func supportPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "stub.tmpl", map[string]interface{}{"Title": "Support", "Stub": "Support", "Invite": "https://discord.gg/UXj5QfCX88"})
}
func forumPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "stub.tmpl", map[string]interface{}{"Title": "Forum", "Stub": "Forum"})
}
func marketplacePage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "stub.tmpl", map[string]interface{}{"Title": "Marketplace", "Stub": "Marketplace"})
}
func keymasterPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "stub.tmpl", map[string]interface{}{"Title": "Portal", "Stub": "Portal (keymaster)"})
}

func docsPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "docs.tmpl", map[string]interface{}{"Title": "Documentation"})
}

func apiServersLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(liveServers())
}

// ---- router assembly (Go 1.22 patterns) ----

func buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerUIRoutes(mux)

	// static
	static, _ := fs.Sub(siteFS, "site/static")
	mux.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.FS(static))))
	mux.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.FS(static))))
	mux.Handle("/img/", http.StripPrefix("/img/", http.FileServer(http.FS(static))))

	// pages
	mux.HandleFunc("GET /{$}", homePage)
	mux.HandleFunc("GET /servers", serversPage)
	mux.HandleFunc("GET /servers/{ep}", serverDetailPage)
	mux.HandleFunc("POST /servers/{ep}/upvote", func(w http.ResponseWriter, r *http.Request) { upvoteServer(w, r, r.PathValue("ep")) })
	mux.HandleFunc("GET /login", loginPage)
	mux.HandleFunc("POST /login", apiLogin)
	mux.HandleFunc("GET /register", registerPage)
	mux.HandleFunc("POST /register", apiRegister)
	mux.HandleFunc("GET /logout", logoutPage)
	mux.HandleFunc("GET /account", accountPage)
	mux.HandleFunc("POST /account/password", accountChangePassword)
	mux.HandleFunc("GET /docs", docsPage)
	mux.HandleFunc("GET /support", supportPage)
	mux.HandleFunc("GET /forum", forumPage)
	mux.HandleFunc("GET /marketplace", marketplacePage)
	mux.HandleFunc("GET /keymaster", keymasterPage)

	// staff admin
	mux.HandleFunc("GET /admin/users", adminUsersPage)
	mux.HandleFunc("POST /admin/users/{id}/role", adminSetRole)

	// JSON API (client + site)
	mux.HandleFunc("GET /api/auth/me", apiMe)
	mux.HandleFunc("POST /api/auth/login", apiLogin)
	mux.HandleFunc("POST /api/auth/register", apiRegister)
	mux.HandleFunc("POST /api/auth/logout", apiLogout)
	mux.HandleFunc("GET /api/servers/live", apiServersLive)

	// updater: no updates in local mode
	mux.HandleFunc("/updates/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("UPDATE %s %s -> 404 (keeping current build)", r.Method, r.URL.Path)
		http.Error(w, "no updates (NSM local stack - keeping current build)", http.StatusNotFound)
	})

	// game-cache proxy routes
	for route, upstream := range proxyRoutes {
		rt, up := route, upstream
		mux.HandleFunc("/"+rt+"/", func(w http.ResponseWriter, r *http.Request) {
			serveProxy(w, r, rt, up)
		})
	}

	// everything else -> masterlist v2 handler (api/servers, ingress, ticket, /v1/*, counts, /admin/* bearer)
	mux.Handle("/", &handler{})

	return mux
}
