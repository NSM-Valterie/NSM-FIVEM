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
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed site
var siteFS embed.FS

var tmpl *template.Template

var colorCodeRe = regexp.MustCompile(`\^[0-9]`)

func cleanHostname(s string) string {
	s = strings.TrimSpace(colorCodeRe.ReplaceAllString(s, ""))
	if s == "" {
		return "NSM Dedicated Server"
	}
	return s
}

func initSite() {
	if forum == nil {
		loadForum()
	}
	tmpl = template.Must(template.ParseFS(siteFS, "site/templates/*.tmpl"))
}

func accountCount() int64 {
	accMu.Lock()
	defer accMu.Unlock()
	if acc == nil {
		return 0
	}
	return int64(len(acc.Users))
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
	if _, hasStats := data["Stats"]; !hasStats {
		data["Stats"] = map[string]int64{
			"Players":  totalPlayers(),
			"Servers":  int64(len(reg.live())),
			"Accounts": accountCount(),
			"Posts":    totalForumPosts(),
		}
	}
	data["HasPendingClientAuth"] = hasPendingClientAuth()
	data["Year"] = time.Now().Year()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

// liveServers returns the current masterlist registry entries for the site and JSON API.
func liveServers() []map[string]interface{} {
	out := []map[string]interface{}{}
	for ep, e := range reg.live() {
		d := e.data
		applyMods(ep, &d)
		clean := cleanHostname(d.Hostname)
		initial := "S"
		if len(clean) > 0 {
			initial = strings.ToUpper(clean[:1])
		}
		desc := d.Vars["sv_projectDesc"]
		if desc == "" {
			desc = d.Vars["sv_projectName"]
		}
		var tags []string
		if rawTags := d.Vars["tags"]; rawTags != "" {
			for _, t := range strings.Split(rawTags, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}
		upv := upvoteCount(ep) + int(d.UpvotePower)
		out = append(out, map[string]interface{}{
			// Template fields
			"EndPoint":   ep,
			"Hostname":   d.Hostname,
			"CleanName":  clean,
			"Initial":    initial,
			"Desc":       desc,
			"Clients":    d.Clients,
			"MaxClients": d.SVMaxclients,
			"GameType":   d.Gametype,
			"MapName":    d.Mapname,
			"Upvotes":    upv,
			"Tags":       tags,
			"Resources":  len(d.Resources),
			// Client JS fields
			"id":         ep,
			"name":       clean,
			"desc":       desc,
			"players":    d.Clients,
			"maxPlayers": d.SVMaxclients,
			"mode":       d.Gametype,
			"map":        d.Mapname,
			"tags":       tags,
			"upvotes":    upv,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		ci, cj := out[i]["Clients"].(int64), out[j]["Clients"].(int64)
		if ci != cj {
			return ci > cj
		}
		ui, uj := out[i]["Upvotes"].(int), out[j]["Upvotes"].(int)
		if ui != uj {
			return ui > uj
		}
		return out[i]["CleanName"].(string) < out[j]["CleanName"].(string)
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
	allServers := liveServers()
	topServers := allServers
	if len(topServers) > 6 {
		topServers = topServers[:6]
	}
	renderPage(w, r, "index.tmpl", map[string]interface{}{
		"Title":       "NSM — GTA V Multiplayer Modification & Server Platform",
		"Feed":        feedItems,
		"Servers":     topServers,
		"ForumTopics": recentForumTopics(4),
		"Stats": map[string]int64{
			"Players":  totalPlayers(),
			"Servers":  int64(len(allServers)),
			"Accounts": accountCount(),
			"Posts":    totalForumPosts(),
		},
	})
}

func serversPage(w http.ResponseWriter, r *http.Request) {
	srvs := liveServers()
	tagSet := map[string]bool{}
	var allTags []string
	for _, s := range srvs {
		if tags, ok := s["Tags"].([]string); ok {
			for _, t := range tags {
				if !tagSet[t] {
					tagSet[t] = true
					allTags = append(allTags, t)
				}
			}
		}
	}
	sort.Strings(allTags)
	renderPage(w, r, "servers.tmpl", map[string]interface{}{
		"Title":   "Server List",
		"Servers": srvs,
		"Tags":    allTags,
	})
}

func serverDetailPage(w http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	d := reg.get(ep)
	if d == nil {
		renderPage(w, r, "server_missing.tmpl", map[string]interface{}{"Title": "Server Offline", "EndPoint": ep})
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
	var ownerUsername string
	reg.mu.RLock()
	if ent, ok := reg.servers[ep]; ok && ent.ownerID != 0 {
		accMu.Lock()
		if ou := userByID(ent.ownerID); ou != nil {
			ownerUsername = ou.Username
		}
		accMu.Unlock()
	}
	reg.mu.RUnlock()

	renderPage(w, r, "server.tmpl", map[string]interface{}{
		"Title":         cleanHostname(d.Hostname),
		"CleanName":     cleanHostname(d.Hostname),
		"EndPoint":      ep,
		"Srv":           d,
		"Upvotes":       upvoteCount(ep) + int(d.UpvotePower),
		"Voted":         voted,
		"OwnerUsername": ownerUsername,
	})
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	renderPage(w, r, "login.tmpl", map[string]interface{}{"Title": "Log in", "Next": next})
}

func registerPage(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	renderPage(w, r, "register.tmpl", map[string]interface{}{"Title": "Create account", "Next": next})
}

func logoutPage(w http.ResponseWriter, r *http.Request) {
	apiLogout(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func accountPage(w http.ResponseWriter, r *http.Request) {
	renderAccountPageWithMsg(w, r, currentUser(r), "", "")
}

func supportPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "support.tmpl", map[string]interface{}{
		"Title":  "Support & Status",
		"Invite": "https://discord.gg/UXj5QfCX88",
	})
}

func marketplacePage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/forum/c/releases", http.StatusSeeOther)
}

func keymasterPage(w http.ResponseWriter, r *http.Request) {
	serveKeymasterPage(w, r, nil)
}

func docsPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "docs.tmpl", map[string]interface{}{"Title": "Documentation"})
}

func apiServersLive(w http.ResponseWriter, r *http.Request) {
	srvs := liveServers()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"servers": srvs,
		"players": totalPlayers(),
		"count":   len(srvs),
	})
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
	mux.HandleFunc("GET /signup", registerPage)
	mux.HandleFunc("POST /signup", apiRegister)
	mux.HandleFunc("/f/signup", registerPage)
	mux.HandleFunc("GET /logout", logoutPage)
	mux.HandleFunc("GET /account", accountPage)
	mux.HandleFunc("POST /account/password", accountChangePassword)
	mux.HandleFunc("POST /account/profile", accountUpdateProfile)
	mux.HandleFunc("POST /account/connect-client", accountConnectClient)
	mux.HandleFunc("POST /account/claim-owner", accountClaimOwner)
	mux.HandleFunc("GET /u/check_email.json", serveDiscourseCheckEmail)
	mux.HandleFunc("GET /u/check_username.json", serveDiscourseCheckUsername)
	mux.HandleFunc("POST /u", serveDiscourseCreateUser)
	mux.HandleFunc("GET /u/{username}", userProfilePage)
	mux.HandleFunc("GET /avatars/u/{username}/{size}", serveUserAvatar)
	mux.HandleFunc("/f/avatars/u/{username}/{size}", serveUserAvatar)

	mux.HandleFunc("GET /docs", docsPage)
	mux.HandleFunc("GET /support", supportPage)
	mux.HandleFunc("GET /marketplace", marketplacePage)

	// Forum (full Discourse-style forum at /forum + /f/ compatibility)
	mux.HandleFunc("GET /forum", forumPage)
	mux.HandleFunc("GET /forum/c/{cat}", forumPage)
	mux.HandleFunc("POST /forum/new", forumCreateTopic)
	mux.HandleFunc("GET /forum/t/{id}", forumTopicPage)
	mux.HandleFunc("POST /forum/t/{id}/reply", forumReplyTopic)
	mux.HandleFunc("POST /forum/t/{id}/like/{postId}", forumLikePost)
	mux.HandleFunc("POST /forum/t/{id}/mod/{action}", forumModerateTopic)

	// Keymaster
	mux.HandleFunc("GET /keymaster", keymasterPage)
	mux.HandleFunc("POST /keymaster/create", keymasterCreateForm)
	mux.HandleFunc("POST /keymaster/{id}/revoke", keymasterRevokeForm)

	// In-App Client Login & Discourse API Bridge
	mux.HandleFunc("/user-api-key/new", serveUserAPIKeyNew)
	mux.HandleFunc("/f/user-api-key/new", serveUserAPIKeyNew)
	mux.HandleFunc("/user-api-key", serveUserAPIKeyCreate)
	mux.HandleFunc("/f/user-api-key", serveUserAPIKeyCreate)
	mux.HandleFunc("/api/auth/complete", serveClientAuthComplete)
	mux.HandleFunc("/api/auth/client-poll", apiClientAuthPoll)
	mux.HandleFunc("/session/csrf.json", serveDiscourseCSRF)
	mux.HandleFunc("/f/session/csrf.json", serveDiscourseCSRF)
	mux.HandleFunc("/session/hp.json", serveDiscourseHoneypot)
	mux.HandleFunc("/f/session/hp.json", serveDiscourseHoneypot)
	mux.HandleFunc("/session/current.json", serveDiscourseCurrentSession)
	mux.HandleFunc("/f/session/current.json", serveDiscourseCurrentSession)
	mux.HandleFunc("/session", serveDiscourseCreateSession)
	mux.HandleFunc("/f/session", serveDiscourseCreateSession)
	mux.HandleFunc("/session/{username}", serveDiscourseCreateSession)
	mux.HandleFunc("/f/session/{username}", serveDiscourseCreateSession)
	mux.HandleFunc("/f/u/check_email.json", serveDiscourseCheckEmail)
	mux.HandleFunc("/f/u/check_username.json", serveDiscourseCheckUsername)
	mux.HandleFunc("/f/u", serveDiscourseCreateUser)
	mux.HandleFunc("/f/u/{username}", func(w http.ResponseWriter, r *http.Request) {
		serveDiscourseUserJSON(w, r, r.PathValue("username"))
	})
	mux.HandleFunc("/site", serveDiscourseSiteJSON)
	mux.HandleFunc("/site.json", serveDiscourseSiteJSON)
	mux.HandleFunc("/f/site", serveDiscourseSiteJSON)
	mux.HandleFunc("/f/site.json", serveDiscourseSiteJSON)
	mux.HandleFunc("/posts.json", serveDiscoursePostsJSON)
	mux.HandleFunc("/f/posts.json", serveDiscoursePostsJSON)
	mux.HandleFunc("/f/t/{id}", forumTopicPage)
	mux.HandleFunc("/l/api/validate/discourse", serveValidateDiscourse)

	// staff admin
	mux.HandleFunc("GET /admin/users", adminUsersPage)
	mux.HandleFunc("POST /admin/users/{id}/role", adminSetRole)

	// JSON API (client + site + keymaster)
	mux.HandleFunc("GET /api/auth/me", apiMe)
	mux.HandleFunc("POST /api/auth/login", apiLogin)
	mux.HandleFunc("POST /api/auth/register", apiRegister)
	mux.HandleFunc("POST /api/auth/logout", apiLogout)
	mux.HandleFunc("GET /api/servers/live", apiServersLive)
	mux.HandleFunc("GET /api/keymaster/keys", apiKeymasterList)
	mux.HandleFunc("POST /api/keymaster/issue", apiKeymasterIssue)
	mux.HandleFunc("POST /api/keymaster/revoke", apiKeymasterRevoke)
	mux.HandleFunc("POST /api/nsm/servers/register", apiServerRegister)

	// updater: no updates in local mode
	mux.HandleFunc("/updates/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("UPDATE %s %s -> 404 (keeping current build)", r.Method, r.URL.Path)
		http.Error(w, "no updates (NSM local stack - keeping current build)", http.StatusNotFound)
	})

	// game-cache proxy routes (/f/ and /e/ are handled locally by our Discourse & Forum service)
	for route, upstream := range proxyRoutes {
		rt, up := route, upstream
		if rt == "f" || rt == "e" {
			mux.HandleFunc("/"+rt+"/", serveDiscourseRouter)
			continue
		}
		mux.HandleFunc("/"+rt+"/", func(w http.ResponseWriter, r *http.Request) {
			serveProxy(w, r, rt, up)
		})
	}

	// everything else -> masterlist v2 handler (api/servers, ingress, ticket, /v1/*, counts, /admin/* bearer)
	mux.Handle("/", &handler{})

	return mux
}
