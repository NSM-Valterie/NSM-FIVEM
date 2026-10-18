// NSM Client in-app authentication bridge (cfx-ui Discourse RSA User-Api-Key + legitimacy.dll /l/api/validate/discourse)
// and automatic local UI bundle repair (rewriting the old Discord redirect in citizen/ui.zip to http://127.0.0.1).
package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type pendingAuthReq struct {
	ClientID     string
	Nonce        string
	PublicKeyPEM string
	AppName      string
	AuthRedirect string
	AuthorizedBy int64
	PayloadB64   string
	CreatedAt    time.Time
}

var (
	clientAuthMu         sync.Mutex
	pendingAuthByNonce   = map[string]*pendingAuthReq{}
	latestPendingNonce   string
	lastWebLoginUserID   int64
	lastWebLoginTime     time.Time
	deliveredNonces      = map[string]bool{}
)

func hasPendingClientAuth() bool {
	clientAuthMu.Lock()
	defer clientAuthMu.Unlock()
	if latestPendingNonce == "" {
		return false
	}
	req, ok := pendingAuthByNonce[latestPendingNonce]
	return ok && time.Since(req.CreatedAt) < 30*time.Minute && req.PayloadB64 == ""
}

func recordWebsiteLoginForClientAuth(userID int64) {
	if userID <= 0 {
		return
	}
	clientAuthMu.Lock()
	lastWebLoginUserID = userID
	lastWebLoginTime = time.Now()
	var toAuthorize []*pendingAuthReq
	for _, req := range pendingAuthByNonce {
		if time.Since(req.CreatedAt) < 30*time.Minute && req.AuthorizedBy == 0 {
			req.AuthorizedBy = userID
			toAuthorize = append(toAuthorize, req)
		}
	}
	clientAuthMu.Unlock()

	for _, req := range toAuthorize {
		if payload, err := buildEncryptedClientPayload(userID, req.ClientID, req.Nonce, req.PublicKeyPEM, req.AppName); err == nil {
			clientAuthMu.Lock()
			req.PayloadB64 = payload
			clientAuthMu.Unlock()
		}
	}
}

func parseRSAPublicKeyPEM(pemStr string) (*rsa.PublicKey, error) {
	pemStr = strings.TrimSpace(pemStr)
	if pemStr == "" {
		return nil, errors.New("missing public_key")
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("invalid public_key PEM")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
	}
	if rsaPub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return rsaPub, nil
	}
	return nil, errors.New("unsupported RSA public key format")
}

func issueClientApiKey(userID int64, clientID, appName string) string {
	accMu.Lock()
	defer accMu.Unlock()
	if acc.ClientTokens == nil {
		acc.ClientTokens = map[string]ClientToken{}
	}
	if clientID == "" {
		b := make([]byte, 16)
		rand.Read(b)
		clientID = hex.EncodeToString(b)
	}
	// Reuse existing token for same (userID, clientID) if present
	for tok, ct := range acc.ClientTokens {
		if ct.UserID == userID && ct.ClientID == clientID {
			ct.LastUsed = time.Now().Unix()
			acc.ClientTokens[tok] = ct
			saveAccountsLocked()
			return tok
		}
	}
	raw := make([]byte, 24)
	rand.Read(raw)
	tok := "nsm_uapi_" + hex.EncodeToString(raw)
	acc.ClientTokens[tok] = ClientToken{
		Token:     tok,
		UserID:    userID,
		ClientID:  clientID,
		AppName:   appName,
		CreatedAt: time.Now().Unix(),
		LastUsed:  time.Now().Unix(),
	}
	saveAccountsLocked()
	return tok
}

func buildEncryptedClientPayload(userID int64, clientID, nonce, pubKeyPEM, appName string) (string, error) {
	rsaPub, err := parseRSAPublicKeyPEM(pubKeyPEM)
	if err != nil {
		return "", err
	}
	apiKey := issueClientApiKey(userID, clientID, appName)
	plainObj := map[string]string{
		"key":   apiKey,
		"nonce": nonce,
	}
	plainBytes, _ := json.Marshal(plainObj)
	cipherBytes, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, plainBytes)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(cipherBytes), nil
}

func userFromClientRequest(r *http.Request) *User {
	apiKey := strings.TrimSpace(r.Header.Get("User-Api-Key"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(r.URL.Query().Get("api_key"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if apiKey != "" {
		accMu.Lock()
		if acc != nil && acc.ClientTokens != nil {
			if ct, ok := acc.ClientTokens[apiKey]; ok {
				ct.LastUsed = time.Now().Unix()
				acc.ClientTokens[apiKey] = ct
				u := userByID(ct.UserID)
				if u != nil {
					cp := *u
					accMu.Unlock()
					return &cp
				}
			}
		}
		accMu.Unlock()
	}
	if u := currentUser(r); u != nil {
		accMu.Lock()
		cp := *u
		accMu.Unlock()
		return &cp
	}
	return nil
}

// ---- /user-api-key/new and /f/user-api-key/new ----

func serveUserAPIKeyNew(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	nonce := strings.TrimSpace(r.FormValue("nonce"))
	pubKey := strings.TrimSpace(r.FormValue("public_key"))
	appName := strings.TrimSpace(r.FormValue("application_name"))
	authRedirect := strings.TrimSpace(r.FormValue("auth_redirect"))

	if appName == "" {
		appName = "NSM Client"
	}
	if authRedirect == "" || strings.HasPrefix(authRedirect, "fivem://") {
		authRedirect = "nsm://accept-auth"
	}

	clientAuthMu.Lock()
	if nonce != "" {
		req, exists := pendingAuthByNonce[nonce]
		if !exists {
			req = &pendingAuthReq{CreatedAt: time.Now()}
			pendingAuthByNonce[nonce] = req
		}
		if clientID != "" {
			req.ClientID = clientID
		}
		req.Nonce = nonce
		if pubKey != "" {
			req.PublicKeyPEM = pubKey
		}
		req.AppName = appName
		req.AuthRedirect = authRedirect
		latestPendingNonce = nonce
	} else if latestPendingNonce != "" {
		if req, ok := pendingAuthByNonce[latestPendingNonce]; ok {
			clientID = req.ClientID
			nonce = req.Nonce
			pubKey = req.PublicKeyPEM
			appName = req.AppName
			authRedirect = req.AuthRedirect
		}
	}
	clientAuthMu.Unlock()

	u := currentUser(r)

	// Handle POST (either login, register, or authorize button on the client_auth page)
	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		if u == nil && (action == "login" || action == "register") {
			un := strings.TrimSpace(r.FormValue("username"))
			pw := r.FormValue("password")
			email := strings.TrimSpace(r.FormValue("email"))
			if action == "register" {
				if len(un) < 3 || len(pw) < 6 {
					renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
						"Title": "Sign In to NSM Client", "Error": "Username needs 3+ characters and password needs 6+ characters.",
						"ClientID": clientID, "Nonce": nonce, "PublicKey": pubKey, "AppName": appName, "AuthRedirect": authRedirect,
					})
					return
				}
				accMu.Lock()
				if findUser(un) != nil {
					accMu.Unlock()
					renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
						"Title": "Sign In to NSM Client", "Error": "That username is already taken.",
						"ClientID": clientID, "Nonce": nonce, "PublicKey": pubKey, "AppName": appName, "AuthRedirect": authRedirect,
					})
					return
				}
				role := "user"
				if strings.EqualFold(un, ownerName) {
					role = "owner"
				}
				now := time.Now().UnixMilli()
				newUser := User{
					ID: acc.NextID, Username: un, DisplayName: un, Email: email,
					Pass: bcryptHash(pw), Role: role, CreatedAt: now, UpdatedAt: now, LastLogin: now,
				}
				acc.NextID++
				acc.Users = append(acc.Users, newUser)
				tok := newSession(newUser.ID)
				saveAccountsLocked()
				accMu.Unlock()
				http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
				u = &newUser
			} else {
				accMu.Lock()
				found := findUser(un)
				if found == nil || !bcryptCheck(found.Pass, pw) {
					accMu.Unlock()
					renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
						"Title": "Sign In to NSM Client", "Error": "Wrong username or password.",
						"ClientID": clientID, "Nonce": nonce, "PublicKey": pubKey, "AppName": appName, "AuthRedirect": authRedirect,
					})
					return
				}
				found.LastLogin = time.Now().UnixMilli()
				tok := newSession(found.ID)
				cp := *found
				saveAccountsLocked()
				accMu.Unlock()
				http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
				u = &cp
			}
		}

		if u != nil {
			recordWebsiteLoginForClientAuth(u.ID)
			var payloadB64 string
			var err error
			if pubKey != "" && nonce != "" {
				payloadB64, err = buildEncryptedClientPayload(u.ID, clientID, nonce, pubKey, appName)
				if err == nil {
					clientAuthMu.Lock()
					if req, ok := pendingAuthByNonce[nonce]; ok {
						req.AuthorizedBy = u.ID
						req.PayloadB64 = payloadB64
					}
					clientAuthMu.Unlock()
				}
			}
			var redirectURI, fallbackURI string
			if payloadB64 != "" {
				q := "payload=" + url.QueryEscape(payloadB64)
				redirectURI = "nsm://accept-auth?" + q
				fallbackURI = "fivem://accept-auth?" + q
			}
			renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
				"Title":        "Authorized NSM Client",
				"User":         u,
				"Authorized":   true,
				"RedirectURI":  redirectURI,
				"FallbackURI":  fallbackURI,
				"ClientID":     clientID,
				"Nonce":        nonce,
				"PublicKey":    pubKey,
				"AppName":      appName,
				"AuthRedirect": authRedirect,
			})
			return
		}
	}

	renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
		"Title":        "Sign In to NSM Client",
		"User":         u,
		"Authorized":   false,
		"ClientID":     clientID,
		"Nonce":        nonce,
		"PublicKey":    pubKey,
		"AppName":      appName,
		"AuthRedirect": authRedirect,
	})
}

func bcryptHash(pw string) string {
	b, _ := bcrypt.GenerateFromPassword([]byte(pw), 10)
	return string(b)
}

func bcryptCheck(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// /account/connect-client allows a signed-in user on /account to push their session to a waiting NSM client.
func accountConnectClient(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	recordWebsiteLoginForClientAuth(u.ID)
	renderAccountPageWithMsg(w, r, u, "NSM Client sign-in authorized! If the NSM app is open on the Link Account screen, it will sign you in automatically within 1 second.", "")
}

// /api/auth/client-poll is polled by the NSM client UI while waiting on the Link Account modal.
func apiClientAuthPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Nonce     string `json:"nonce"`
		ClientID  string `json:"clientId"`
		PublicKey string `json:"publicKey"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	nonce := strings.TrimSpace(body.Nonce)
	if nonce == "" {
		nonce = strings.TrimSpace(r.URL.Query().Get("nonce"))
	}
	clientID := strings.TrimSpace(body.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(r.URL.Query().Get("client_id"))
	}
	pubKey := strings.TrimSpace(body.PublicKey)

	if nonce == "" {
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": false})
		return
	}

	clientAuthMu.Lock()
	if deliveredNonces[nonce] {
		clientAuthMu.Unlock()
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "delivered": true})
		return
	}
	req, exists := pendingAuthByNonce[nonce]
	if !exists {
		req = &pendingAuthReq{
			ClientID:     clientID,
			Nonce:        nonce,
			PublicKeyPEM: pubKey,
			AppName:      "NSM Client",
			CreatedAt:    time.Now(),
		}
		pendingAuthByNonce[nonce] = req
		latestPendingNonce = nonce
	} else {
		if clientID != "" && req.ClientID == "" {
			req.ClientID = clientID
		}
		if pubKey != "" && req.PublicKeyPEM == "" {
			req.PublicKeyPEM = pubKey
		}
	}
	if req.AuthorizedBy == 0 && lastWebLoginUserID != 0 && time.Since(lastWebLoginTime) < 15*time.Minute {
		req.AuthorizedBy = lastWebLoginUserID
	}
	authUserID := req.AuthorizedBy
	cachedPayload := req.PayloadB64
	reqClientID := req.ClientID
	reqPubKey := req.PublicKeyPEM
	reqAppName := req.AppName
	clientAuthMu.Unlock()

	if authUserID == 0 {
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "waiting": true})
		return
	}

	if cachedPayload == "" && reqPubKey != "" {
		if p, err := buildEncryptedClientPayload(authUserID, reqClientID, nonce, reqPubKey, reqAppName); err == nil {
			cachedPayload = p
			clientAuthMu.Lock()
			req.PayloadB64 = p
			clientAuthMu.Unlock()
		}
	}

	if cachedPayload == "" {
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "waiting": true})
		return
	}

	clientAuthMu.Lock()
	deliveredNonces[nonce] = true
	clientAuthMu.Unlock()

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"payload": cachedPayload,
	})
}

// ---- Discourse-compatible endpoints consumed by citizen/ui.zip & legitimacy.dll ----

func discourseGroupsForUser(u *User) []map[string]interface{} {
	groups := []map[string]interface{}{
		{"id": 10, "name": "trust_level_1"},
		{"id": 20, "name": "premium_nsm"},
		{"id": 21, "name": "patreon_enduser"},
	}
	if u != nil && roleAtLeast(u.Role, "staff") {
		groups = append(groups, map[string]interface{}{"id": 3, "name": "staff"})
	}
	return groups
}

func serveDiscourseCurrentSession(w http.ResponseWriter, r *http.Request) {
	u := userFromClientRequest(r)
	if u == nil {
		sendJSON(w, http.StatusForbidden, map[string]interface{}{"error": "not_logged_in"})
		return
	}
	isStaff := roleAtLeast(u.Role, "staff")
	isAdmin := roleAtLeast(u.Role, "admin")
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"current_user": map[string]interface{}{
			"id":                 u.ID,
			"username":           u.Username,
			"name":               u.EffectiveDisplayName(),
			"avatar_template":    u.AvatarTemplate(),
			"title":              u.EffectiveTitle(),
			"admin":              isAdmin,
			"moderator":          isStaff,
			"staff":              isStaff,
			"trust_level":        4,
			"primary_group_name": u.Role,
		},
	})
}

func serveDiscourseUserJSON(w http.ResponseWriter, r *http.Request, username string) {
	username = strings.TrimSuffix(username, ".json")
	accMu.Lock()
	found := findUser(username)
	var u *User
	if found != nil {
		cp := *found
		u = &cp
	}
	accMu.Unlock()
	if u == nil {
		u = userFromClientRequest(r)
	}
	if u == nil {
		sendJSON(w, http.StatusNotFound, map[string]interface{}{"error": "not_found"})
		return
	}
	isStaff := roleAtLeast(u.Role, "staff")
	isAdmin := roleAtLeast(u.Role, "admin")
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":              u.ID,
			"username":        u.Username,
			"name":            u.EffectiveDisplayName(),
			"title":           u.EffectiveTitle(),
			"bio_raw":         u.Bio,
			"bio_cooked":      u.Bio,
			"location":        u.Location,
			"website":         u.Website,
			"avatar_template": u.AvatarTemplate(),
			"admin":           isAdmin,
			"moderator":       isStaff,
			"trust_level":     4,
			"groups":          discourseGroupsForUser(u),
		},
	})
}

func serveDiscourseSiteJSON(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"default_archetype": "regular",
		"groups": []map[string]interface{}{
			{"id": 3, "name": "staff"},
			{"id": 20, "name": "premium_nsm"},
			{"id": 21, "name": "patreon_enduser"},
		},
		"categories":     forumCategoriesJSON(),
		"auth_providers": []interface{}{},
	})
}

// serveValidateDiscourse handles /l/api/validate/discourse for legitimacy.dll (ConnectToNative.cpp).
func serveValidateDiscourse(w http.ResponseWriter, r *http.Request) {
	u := userFromClientRequest(r)
	if u == nil {
		// Check if there is an active client token in accounts.json from the current client
		accMu.Lock()
		for _, ct := range acc.ClientTokens {
			if found := userByID(ct.UserID); found != nil {
				cp := *found
				u = &cp
				break
			}
		}
		accMu.Unlock()
	}
	if u == nil {
		sendJSON(w, http.StatusForbidden, map[string]string{"error": "invalid_token"})
		return
	}
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":       u.ID,
			"username": u.Username,
			"name":     u.EffectiveDisplayName(),
			"groups":   discourseGroupsForUser(u),
		},
	})
}

// ---- Automatic Local UI Bundle Repair (citizen/ui.zip & citizen/ui-big.zip) ----

const uiClientAuthPollSnippet = `<script id="nsm-client-auth-poll">(function(){var done="";setInterval(function(){try{var n=window.localStorage.getItem("lastAuthNonce")||"";var c=window.localStorage.getItem("clientId")||"";var t=window.localStorage.getItem("discourseAuthToken")||"";if(!n||t||n===done)return;var p="";try{var k=JSON.parse(window.localStorage.getItem("rsaKeys")||"{}");p=k.public||"";}catch(e){}fetch("http://127.0.0.1/api/auth/client-poll?nonce="+encodeURIComponent(n)+"&client_id="+encodeURIComponent(c),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({nonce:n,clientId:c,publicKey:p})}).then(function(r){return r.json();}).then(function(d){if(d&&d.ok&&d.payload&&n!==done){done=n;window.postMessage({type:"authPayload",data:"payload="+encodeURIComponent(d.payload)},"*");}}).catch(function(){});}catch(e){}},1200);})();</script>`

func patchUIBundleBytes(raw []byte) ([]byte, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, false, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	changed := false

	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, false, err
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, false, err
		}

		lower := strings.ToLower(f.Name)
		if strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".json") {
			orig := body
			// Disable Sentry if present
			body = bytes.ReplaceAll(body,
				[]byte("init({dsn:`http://127.0.0.1/sentry-ingest`,tracesSampleRate:.05"),
				[]byte("init({enabled:!1,dsn:void 0,tracesSampleRate:0"))
			// Point all Discord invite replacements inside the UI bundle to the local NSM website
			body = bytes.ReplaceAll(body, []byte("https://discord.gg/UXj5QfCX88/terms"), []byte("http://127.0.0.1/docs"))
			body = bytes.ReplaceAll(body, []byte("https://discord.gg/UXj5QfCX88"), []byte("http://127.0.0.1"))
			body = bytes.ReplaceAll(body, []byte("https://forum.cfx.re"), []byte("http://127.0.0.1"))
			body = bytes.ReplaceAll(body, []byte("https://portal.cfx.re"), []byte("http://127.0.0.1/keymaster"))

			if strings.HasSuffix(lower, ".html") && !bytes.Contains(body, []byte(`id="nsm-client-auth-poll"`)) {
				if bytes.Contains(body, []byte("</body>")) {
					body = bytes.Replace(body, []byte("</body>"), []byte(uiClientAuthPollSnippet+"</body>"), 1)
				} else {
					body = append(body, []byte(uiClientAuthPollSnippet)...)
				}
			}
			if !bytes.Equal(body, orig) {
				changed = true
			}
		}

		hdr := f.FileHeader
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, false, err
		}
		if _, err := w.Write(body); err != nil {
			return nil, false, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, false, err
	}
	return out.Bytes(), changed, nil
}

func patchLocalUIBundlesOnDisk() {
	candidates := []string{
		"citizen/ui.zip",
		"citizen/ui-big.zip",
		"NSM.app/citizen/ui.zip",
		"NSM.app/citizen/ui-big.zip",
		"../citizen/ui.zip",
		"../citizen/ui-big.zip",
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		patched, changed, err := patchUIBundleBytes(raw)
		if err != nil || !changed {
			continue
		}
		tmp := p + ".nsm.tmp"
		if err := os.WriteFile(tmp, patched, 0644); err == nil {
			if err := os.Rename(tmp, p); err == nil {
				log.Printf("[ui-patch] updated %s -> linked in-app login & portal to http://127.0.0.1", p)
			} else {
				_ = os.Remove(tmp)
			}
		}
	}
}
