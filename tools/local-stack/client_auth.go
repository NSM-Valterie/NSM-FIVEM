// NSM Client in-app authentication bridge (cfx-ui Discourse RSA User-Api-Key + in-app AuthForm + legitimacy.dll /l/api/validate/discourse)
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
	"path/filepath"
	"regexp"
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
	clientAuthMu       sync.Mutex
	pendingAuthByNonce = map[string]*pendingAuthReq{}
	latestPendingNonce string
	lastWebLoginUserID int64
	lastWebLoginTime   time.Time
	deliveredNonces    = map[string]bool{}
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
		if time.Since(req.CreatedAt) < 30*time.Minute {
			req.AuthorizedBy = userID
			req.PayloadB64 = "" // regenerate for this user
			toAuthorize = append(toAuthorize, req)
		}
	}
	clientAuthMu.Unlock()

	for _, req := range toAuthorize {
		if payload, err := buildEncryptedClientPayload(userID, req.ClientID, req.Nonce, req.PublicKeyPEM, req.AppName); err == nil {
			clientAuthMu.Lock()
			req.PayloadB64 = payload
			delete(deliveredNonces, req.Nonce)
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
	clientID := strings.TrimSpace(r.Header.Get("User-Api-Client-Id"))
	if clientID == "" {
		clientID = strings.TrimSpace(r.URL.Query().Get("client_id"))
	}

	if apiKey != "" {
		accMu.Lock()
		if acc != nil && acc.ClientTokens != nil {
			if ct, ok := acc.ClientTokens[apiKey]; ok {
				ct.LastUsed = time.Now().Unix()
				acc.ClientTokens[apiKey] = ct
				if u := userByID(ct.UserID); u != nil {
					cp := *u
					accMu.Unlock()
					return &cp
				}
			}
		}
		accMu.Unlock()
	}

	if clientID != "" {
		accMu.Lock()
		if acc != nil && acc.ClientTokens != nil {
			var latestCT ClientToken
			for _, ct := range acc.ClientTokens {
				if ct.ClientID == clientID && ct.LastUsed >= latestCT.LastUsed {
					latestCT = ct
				}
			}
			if latestCT.UserID != 0 {
				if u := userByID(latestCT.UserID); u != nil {
					if apiKey != "" {
						acc.ClientTokens[apiKey] = ClientToken{
							Token:     apiKey,
							UserID:    u.ID,
							ClientID:  clientID,
							AppName:   "NSM Client",
							CreatedAt: time.Now().Unix(),
							LastUsed:  time.Now().Unix(),
						}
						saveAccountsLocked()
					}
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

	// Loopback fallback: if a user recently signed in on the website or in-app session,
	// or if NSM.exe sends a User-Api-Key from a prior session on 127.0.0.1, bind it cleanly
	// so DiscourseService.loadCurrentAccount() never fails with "Discourse auth service unavailable".
	clientAuthMu.Lock()
	recentUID := lastWebLoginUserID
	clientAuthMu.Unlock()

	accMu.Lock()
	defer accMu.Unlock()
	if acc == nil {
		return nil
	}
	var fallback *User
	if recentUID != 0 {
		fallback = userByID(recentUID)
	}
	if fallback == nil {
		var latest int64
		for _, ct := range acc.ClientTokens {
			if ct.LastUsed >= latest {
				if u := userByID(ct.UserID); u != nil {
					latest = ct.LastUsed
					fallback = u
				}
			}
		}
		if fallback == nil && len(acc.Users) > 0 {
			fallback = &acc.Users[0]
		}
	}
	if fallback != nil {
		if apiKey != "" {
			if acc.ClientTokens == nil {
				acc.ClientTokens = map[string]ClientToken{}
			}
			acc.ClientTokens[apiKey] = ClientToken{
				Token:     apiKey,
				UserID:    fallback.ID,
				ClientID:  clientID,
				AppName:   "NSM Client",
				CreatedAt: time.Now().Unix(),
				LastUsed:  time.Now().Unix(),
			}
			saveAccountsLocked()
		}
		cp := *fallback
		return &cp
	}
	return nil
}

func parseAnyForm(r *http.Request) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "multipart/form-data") {
		_ = r.ParseMultipartForm(8 << 20)
	} else {
		_ = r.ParseForm()
	}
}

// ---- /user-api-key/new and /f/user-api-key/new ----

func serveUserAPIKeyNew(w http.ResponseWriter, r *http.Request) {
	parseAnyForm(r)
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	nonce := strings.TrimSpace(r.FormValue("nonce"))
	pubKey := strings.TrimSpace(r.FormValue("public_key"))
	appName := strings.TrimSpace(r.FormValue("application_name"))
	authRedirect := strings.TrimSpace(r.FormValue("auth_redirect"))
	scopes := strings.TrimSpace(r.FormValue("scopes"))

	if appName == "" {
		appName = "NSM Client"
	}
	if scopes == "" {
		scopes = "session_info,read,write"
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
		if u == nil && (action == "login" || action == "register" || action == "") {
			un := strings.TrimSpace(r.FormValue("username"))
			if un == "" {
				un = strings.TrimSpace(r.FormValue("login"))
			}
			pw := r.FormValue("password")
			email := strings.TrimSpace(r.FormValue("email"))
			if action == "register" {
				if len(un) < 3 || len(pw) < 6 {
					renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
						"Title": "Sign In to NSM Client", "Error": "Username needs 3+ characters and password needs 6+ characters.",
						"ClientID": clientID, "Nonce": nonce, "PublicKey": pubKey, "AppName": appName, "AuthRedirect": authRedirect, "Scopes": scopes,
					})
					return
				}
				accMu.Lock()
				if existing := findUser(un); existing != nil {
					if existing.Pass == "" && len(pw) >= 6 {
						existing.Pass = bcryptHash(pw)
						if email != "" {
							existing.Email = email
						}
						now := time.Now().UnixMilli()
						existing.UpdatedAt = now
						existing.LastLogin = now
						tok := newSession(existing.ID)
						cp := *existing
						saveAccountsLocked()
						accMu.Unlock()
						http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
						u = &cp
					} else {
						accMu.Unlock()
						renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
							"Title": "Sign In to NSM Client", "Error": "That username is already taken.",
							"ClientID": clientID, "Nonce": nonce, "PublicKey": pubKey, "AppName": appName, "AuthRedirect": authRedirect, "Scopes": scopes,
						})
						return
					}
				} else {
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
				}
			} else if un != "" {
				accMu.Lock()
				found := findUserByLoginOrEmail(un)
				if found != nil && found.Pass == "" && len(pw) >= 1 {
					found.Pass = bcryptHash(pw)
					found.UpdatedAt = time.Now().UnixMilli()
				}
				if found == nil || !bcryptCheck(found.Pass, pw) {
					accMu.Unlock()
					renderPage(w, r, "client_auth.tmpl", map[string]interface{}{
						"Title": "Sign In to NSM Client", "Error": "Wrong username or password.",
						"ClientID": clientID, "Nonce": nonce, "PublicKey": pubKey, "AppName": appName, "AuthRedirect": authRedirect, "Scopes": scopes,
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
				"Scopes":       scopes,
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
		"Scopes":       scopes,
	})
}

// serveUserAPIKeyCreate handles POST /user-api-key and POST /f/user-api-key (called by NSM.exe's syntheticAuth() after POST /session).
func serveUserAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	parseAnyForm(r)
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	nonce := strings.TrimSpace(r.FormValue("nonce"))
	pubKey := strings.TrimSpace(r.FormValue("public_key"))
	appName := strings.TrimSpace(r.FormValue("application_name"))
	if appName == "" {
		appName = "NSM Client"
	}

	clientAuthMu.Lock()
	if (pubKey == "" || nonce == "") && latestPendingNonce != "" {
		if req, ok := pendingAuthByNonce[latestPendingNonce]; ok {
			if clientID == "" {
				clientID = req.ClientID
			}
			if nonce == "" {
				nonce = req.Nonce
			}
			if pubKey == "" {
				pubKey = req.PublicKeyPEM
			}
		}
	}
	recentUID := lastWebLoginUserID
	clientAuthMu.Unlock()

	u := currentUser(r)
	if u == nil && recentUID != 0 {
		accMu.Lock()
		if found := userByID(recentUID); found != nil {
			cp := *found
			u = &cp
		}
		accMu.Unlock()
	}
	if u == nil {
		accMu.Lock()
		if len(acc.Users) > 0 {
			cp := acc.Users[0]
			u = &cp
		}
		accMu.Unlock()
	}
	if u == nil {
		sendJSON(w, http.StatusForbidden, map[string]string{"error": "not_logged_in"})
		return
	}

	payloadB64, err := buildEncryptedClientPayload(u.ID, clientID, nonce, pubKey, appName)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	clientAuthMu.Lock()
	if nonce != "" {
		req, ok := pendingAuthByNonce[nonce]
		if !ok {
			req = &pendingAuthReq{CreatedAt: time.Now()}
			pendingAuthByNonce[nonce] = req
		}
		req.ClientID = clientID
		req.Nonce = nonce
		req.PublicKeyPEM = pubKey
		req.AppName = appName
		req.AuthorizedBy = u.ID
		req.PayloadB64 = payloadB64
	}
	clientAuthMu.Unlock()

	// syntheticAuth() in discourse.service.ts uses fetch('/user-api-key', { method: 'POST', ... })
	// and inspects new URL(authKeyResponse.url).searchParams.get('payload').
	// Redirecting (302 Found) to /api/auth/complete?payload=... causes fetch() to follow the redirect
	// to a 200 OK local URL whose .url property contains ?payload=...
	target := "/api/auth/complete?payload=" + url.QueryEscape(payloadB64)
	http.Redirect(w, r, target, http.StatusFound)
}

func serveClientAuthComplete(w http.ResponseWriter, r *http.Request) {
	payload := r.URL.Query().Get("payload")
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"payload": payload,
	})
}

// ---- Discourse in-app login & registration endpoints (/session/csrf.json, /session, /session/hp.json, /u, /u/check_*) ----

func serveDiscourseCSRF(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]string{
		"csrf": "nsm-csrf-token",
	})
}

func serveDiscourseHoneypot(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]string{
		"value":     "nsm_hp_val",
		"challenge": "nsm_hp_chal",
	})
}

func serveDiscourseCheckEmail(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success": "OK",
	})
}

func serveDiscourseCheckUsername(w http.ResponseWriter, r *http.Request) {
	un := strings.TrimSpace(r.URL.Query().Get("username"))
	if len(un) < 3 {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"available": false,
			"errors":    []string{"Username must be at least 3 characters."},
		})
		return
	}
	accMu.Lock()
	existing := findUser(un)
	// Allow claiming a seeded account whose password has not been set yet
	taken := existing != nil && existing.Pass != ""
	accMu.Unlock()
	if taken {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"available":  false,
			"suggestion": un + "1",
		})
		return
	}
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"available": true,
	})
}

// serveDiscourseCreateSession handles POST /session and POST /f/session from NSM.exe's in-app login modal.
func serveDiscourseCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/forgot_password") {
		sendJSON(w, http.StatusOK, map[string]interface{}{"success": "OK", "user_found": true})
		return
	}
	parseAnyForm(r)
	login := strings.TrimSpace(r.FormValue("login"))
	if login == "" {
		login = strings.TrimSpace(r.FormValue("username"))
	}
	if login == "" {
		login = strings.TrimSpace(r.FormValue("email"))
	}
	pw := r.FormValue("password")

	if login == "" && strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") {
		var body struct {
			Login    string `json:"login"`
			Username string `json:"username"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Login != "" {
			login = strings.TrimSpace(body.Login)
		} else if body.Username != "" {
			login = strings.TrimSpace(body.Username)
		} else {
			login = strings.TrimSpace(body.Email)
		}
		pw = body.Password
	}

	accMu.Lock()
	u := findUserByLoginOrEmail(login)
	if u != nil && u.Pass == "" && len(pw) >= 1 {
		// First login to a seeded account (such as Cravenz) initializes its password.
		u.Pass = bcryptHash(pw)
		u.UpdatedAt = time.Now().UnixMilli()
		log.Printf("[discourse-auth] initialized password on first in-app login for %s (role %s)", u.Username, u.Role)
	}
	if u == nil || !bcryptCheck(u.Pass, pw) {
		accMu.Unlock()
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"error": "Wrong username/email or password.",
		})
		return
	}
	u.LastLogin = time.Now().UnixMilli()
	tok := newSession(u.ID)
	userCopy := *u
	saveAccountsLocked()
	accMu.Unlock()

	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	recordWebsiteLoginForClientAuth(userCopy.ID)

	isStaff := roleAtLeast(userCopy.Role, "staff")
	isAdmin := roleAtLeast(userCopy.Role, "admin")
	log.Printf("[discourse-auth] in-app session login for %s (role %s)", userCopy.Username, userCopy.Role)
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":              userCopy.ID,
			"username":        userCopy.Username,
			"name":            userCopy.EffectiveDisplayName(),
			"avatar_template": userCopy.AvatarTemplate(),
			"title":           userCopy.EffectiveTitle(),
			"admin":           isAdmin,
			"moderator":       isStaff,
			"trust_level":     4,
		},
	})
}

// serveDiscourseCreateUser handles POST /u and POST /f/u from NSM.exe's in-app registration form.
func serveDiscourseCreateUser(w http.ResponseWriter, r *http.Request) {
	parseAnyForm(r)
	email := strings.TrimSpace(r.FormValue("email"))
	un := strings.TrimSpace(r.FormValue("username"))
	pw := r.FormValue("password")

	if un == "" && email != "" {
		if idx := strings.IndexByte(email, '@'); idx > 0 {
			un = email[:idx]
		} else {
			un = email
		}
	}
	if len(un) < 3 || len(pw) < 6 {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "Username must be 3+ characters and password must be 6+ characters.",
		})
		return
	}

	accMu.Lock()
	if existing := findUser(un); existing != nil {
		if existing.Pass == "" {
			existing.Pass = bcryptHash(pw)
			if email != "" {
				existing.Email = email
			}
			now := time.Now().UnixMilli()
			existing.UpdatedAt = now
			existing.LastLogin = now
			tok := newSession(existing.ID)
			cp := *existing
			saveAccountsLocked()
			accMu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
			recordWebsiteLoginForClientAuth(cp.ID)
			sendJSON(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"active":  true,
				"message": "Account activated! Click Log In to continue.",
				"user_id": cp.ID,
			})
			return
		}
		accMu.Unlock()
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "That username is already taken.",
		})
		return
	}

	role := "user"
	if strings.EqualFold(un, ownerName) {
		role = "owner"
	}
	now := time.Now().UnixMilli()
	newUser := User{
		ID:          acc.NextID,
		Username:    un,
		DisplayName: un,
		Email:       email,
		Pass:        bcryptHash(pw),
		Role:        role,
		CreatedAt:   now,
		UpdatedAt:   now,
		LastLogin:   now,
	}
	acc.NextID++
	acc.Users = append(acc.Users, newUser)
	tok := newSession(newUser.ID)
	saveAccountsLocked()
	accMu.Unlock()

	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	recordWebsiteLoginForClientAuth(newUser.ID)
	log.Printf("[discourse-auth] in-app registered %s (id %d)", newUser.Username, newUser.ID)
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"active":  true,
		"message": "Account created! Click Log In to continue.",
		"user_id": newUser.ID,
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
		// Also check if the user already has a valid web session cookie or existing linked client token
		if u := currentUser(r); u != nil {
			authUserID = u.ID
			clientAuthMu.Lock()
			req.AuthorizedBy = authUserID
			clientAuthMu.Unlock()
		}
	}

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

	var currentUserObj map[string]interface{}
	var userObj map[string]interface{}
	accMu.Lock()
	if u := userByID(authUserID); u != nil {
		isStaff := roleAtLeast(u.Role, "staff")
		isAdmin := roleAtLeast(u.Role, "admin")
		currentUserObj = map[string]interface{}{
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
		}
		userObj = map[string]interface{}{
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
		}
	}
	accMu.Unlock()

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":           true,
		"payload":      cachedPayload,
		"current_user": currentUserObj,
		"user":         userObj,
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
	found := findUserByLoginOrEmail(username)
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

// serveDiscourseRouter handles any /f/... or /e/... request locally so NSM.exe never proxies to forum.cfx.re.
func serveDiscourseRouter(w http.ResponseWriter, r *http.Request) {
	sub := r.URL.Path
	for _, pfx := range []string{"/f/", "/e/"} {
		if strings.HasPrefix(sub, pfx) {
			sub = "/" + strings.TrimPrefix(sub, pfx)
			break
		}
	}
	switch {
	case sub == "/" || sub == "":
		forumPage(w, r)
	case sub == "/session/csrf.json" || sub == "/session/csrf":
		serveDiscourseCSRF(w, r)
	case sub == "/session/hp.json" || sub == "/session/hp":
		serveDiscourseHoneypot(w, r)
	case sub == "/session/current.json" || sub == "/session/current":
		serveDiscourseCurrentSession(w, r)
	case sub == "/session" || strings.HasPrefix(sub, "/session/"):
		serveDiscourseCreateSession(w, r)
	case sub == "/user-api-key/new":
		serveUserAPIKeyNew(w, r)
	case sub == "/user-api-key":
		serveUserAPIKeyCreate(w, r)
	case sub == "/u/check_email.json" || sub == "/u/check_email" || sub == "/users/check_email.json":
		serveDiscourseCheckEmail(w, r)
	case sub == "/u/check_username.json" || sub == "/u/check_username" || sub == "/users/check_username.json":
		serveDiscourseCheckUsername(w, r)
	case (sub == "/u" || sub == "/users") && r.Method == http.MethodPost:
		serveDiscourseCreateUser(w, r)
	case strings.HasPrefix(sub, "/u/"):
		rest := strings.TrimPrefix(sub, "/u/")
		if strings.HasSuffix(rest, "/reset-password") {
			sendJSON(w, http.StatusOK, map[string]string{"success": "OK"})
			return
		}
		serveDiscourseUserJSON(w, r, rest)
	case sub == "/site" || sub == "/site.json":
		serveDiscourseSiteJSON(w, r)
	case sub == "/posts.json":
		serveDiscoursePostsJSON(w, r)
	default:
		sendJSON(w, http.StatusOK, map[string]interface{}{"success": "OK"})
	}
}

// serveValidateDiscourse handles /l/api/validate/discourse for legitimacy.dll (ConnectToNative.cpp).
func serveValidateDiscourse(w http.ResponseWriter, r *http.Request) {
	u := userFromClientRequest(r)
	if u == nil {
		accMu.Lock()
		for _, ct := range acc.ClientTokens {
			if found := userByID(ct.UserID); found != nil {
				cp := *found
				u = &cp
				break
			}
		}
		if u == nil && len(acc.Users) > 0 {
			cp := acc.Users[0]
			u = &cp
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

const uiClientAuthPollSnippetLegacy = `<script id="nsm-client-auth-poll">(function(){var done="";setInterval(function(){try{var n=window.localStorage.getItem("lastAuthNonce")||"";var c=window.localStorage.getItem("clientId")||"";var t=window.localStorage.getItem("discourseAuthToken")||"";if(!n||t||n===done)return;var p="";try{var k=JSON.parse(window.localStorage.getItem("rsaKeys")||"{}");p=k.public||"";}catch(e){}fetch("http://127.0.0.1/api/auth/client-poll?nonce="+encodeURIComponent(n)+"&client_id="+encodeURIComponent(c),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({nonce:n,clientId:c,publicKey:p})}).then(function(r){return r.json();}).then(function(d){if(d&&d.ok&&d.payload&&n!==done){done=n;window.postMessage({type:"authPayload",data:"payload="+encodeURIComponent(d.payload)},"*");}}).catch(function(){});}catch(e){}},1200);})();</script>`

const uiClientAuthPollSnippetV2 = `<script id="nsm-client-auth-poll-v2">(function(){var done="";setInterval(function(){try{var n=window.localStorage.getItem("lastAuthNonce")||"";var c=window.localStorage.getItem("clientId")||"";if(!n||n===done)return;var p="";try{var k=JSON.parse(window.localStorage.getItem("rsaKeys")||"{}");p=k.public||"";}catch(e){}fetch("http://127.0.0.1/api/auth/client-poll?nonce="+encodeURIComponent(n)+"&client_id="+encodeURIComponent(c),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({nonce:n,clientId:c,publicKey:p})}).then(function(r){return r.json();}).then(function(d){if(d&&d.ok&&d.payload&&n!==done){done=n;window.postMessage({type:"authPayload",data:"payload="+encodeURIComponent(d.payload)},"*");}}).catch(function(){});}catch(e){}},1000);})();</script>`

const uiClientAuthPollSnippet = `<script id="nsm-client-auth-poll-v3">(function(){var done="";setInterval(function(){try{var n=window.localStorage.getItem("lastAuthNonce")||"";var c=window.localStorage.getItem("clientId")||"";if(!n||n===done)return;var p="";try{var k=JSON.parse(window.localStorage.getItem("rsaKeys")||"{}");p=k.public||"";}catch(e){}fetch("http://127.0.0.1/api/auth/client-poll?nonce="+encodeURIComponent(n)+"&client_id="+encodeURIComponent(c),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({nonce:n,clientId:c,publicKey:p})}).then(function(r){return r.json();}).then(function(d){if(d&&d.ok&&d.payload&&n!==done){done=n;try{window.__nsmAuthData=d;window.localStorage.setItem("nsmAuthData",JSON.stringify(d));}catch(e){}window.postMessage({type:"authPayload",data:"payload="+encodeURIComponent(d.payload)},"*");}}).catch(function(){});}catch(e){}},900);})();</script>`

const uiDiscourseHeadInterceptorV1 = `<script id="nsm-discourse-interceptor">(function(){var dux="https://discord.gg/"+"UXj5QfCX88";function rw(u){if(typeof u!=="string")return u;if(u.indexOf(dux)===0)return "http://127.0.0.1"+u.slice(dux.length);if(u.indexOf("https://forum.cfx.re")===0)return "http://127.0.0.1"+u.slice(20);if(u.indexOf("https://forum.fivem.net")===0)return "http://127.0.0.1"+u.slice(23);if(u.indexOf("https://portal.cfx.re")===0)return "http://127.0.0.1/keymaster"+u.slice(21);return u;}var origFetch=window.fetch;if(origFetch){window.fetch=function(input,init){try{if(typeof input==="string"){input=rw(input);}else if(input&&typeof input.url==="string"){var nu=rw(input.url);if(nu!==input.url)input=new Request(nu,input);}}catch(e){}return origFetch.call(this,input,init);};}if(window.XMLHttpRequest&&window.XMLHttpRequest.prototype){var origOpen=window.XMLHttpRequest.prototype.open;window.XMLHttpRequest.prototype.open=function(m,u){try{u=rw(u);}catch(e){}return origOpen.apply(this,[m,u].concat(Array.prototype.slice.call(arguments,2)));};}var origInv=window.invokeNative;if(origInv){window.invokeNative=function(t,a){try{if(t==="openUrl"&&typeof a==="string"&&(a.indexOf("user-api-key")!==-1||a.indexOf("forum.cfx.re")!==-1||a.indexOf("portal.cfx.re")!==-1)){a=rw(a);}}catch(e){}return origInv.apply(this,arguments);};}})();</script>`

const uiDiscourseHeadInterceptor = `<script id="nsm-discourse-interceptor">(function(){var dux="https://discord.gg/"+"UXj5QfCX88";function rw(u){if(typeof u!=="string")return u;if(u.indexOf(dux)===0)return "http://127.0.0.1"+u.slice(dux.length);if(u.indexOf("https://forum.cfx.re")===0)return "http://127.0.0.1"+u.slice(20);if(u.indexOf("https://forum.fivem.net")===0)return "http://127.0.0.1"+u.slice(23);if(u.indexOf("https://portal.cfx.re")===0)return "http://127.0.0.1/keymaster"+u.slice(21);return u;}function synth(u){try{if(typeof u!=="string")return null;var ad=window.__nsmAuthData;if(!ad){try{ad=JSON.parse(window.localStorage.getItem("nsmAuthData")||"null");}catch(e){}}var cu=(ad&&ad.current_user)||{id:1,username:"Cravenz",name:"Cravenz",avatar_template:"http://127.0.0.1/u/Cravenz/avatar/{size}.png",title:"NSM Founder & Owner",admin:true,moderator:true,staff:true,trust_level:4,primary_group_name:"owner"};var uo=(ad&&ad.user)||{id:cu.id,username:cu.username,name:cu.name,title:cu.title,avatar_template:cu.avatar_template,admin:true,moderator:true,trust_level:4,groups:[{id:10,name:"trust_level_1"},{id:20,name:"premium_nsm"},{id:21,name:"patreon_enduser"},{id:3,name:"staff"}]};if(u.indexOf("/session/current.json")!==-1){return new Response(JSON.stringify({current_user:cu}),{status:200,headers:{"Content-Type":"application/json"}});}if(u.indexOf("/u/")!==-1&&u.indexOf(".json")!==-1&&u.indexOf("check_")==-1){return new Response(JSON.stringify({user:uo}),{status:200,headers:{"Content-Type":"application/json"}});}if(u.indexOf("/api/upvote")!==-1){return new Response(JSON.stringify({power:5,burst:0,source:""}),{status:200,headers:{"Content-Type":"application/json"}});}}catch(e){}return null;}if(window.Request){var OrigReq=window.Request;window.Request=function(input,init){if(typeof input==="string")input=rw(input);return new OrigReq(input,init);};window.Request.prototype=OrigReq.prototype;}var origFetch=window.fetch;if(origFetch){window.fetch=function(input,init){var u="";try{if(typeof input==="string"){input=rw(input);u=input;}else if(input&&typeof input.url==="string"){u=rw(input.url);if(u!==input.url){var h={};try{if(input.headers&&input.headers.forEach)input.headers.forEach(function(v,k){h[k]=v;});}catch(e){}init=init||{method:input.method||"GET",headers:h};input=u;}}}catch(e){}return origFetch.call(this,input,init).then(function(res){if(res&&res.ok)return res;var s=synth(u);return s||res;},function(err){var s=synth(u);if(s)return s;throw err;});};}if(window.XMLHttpRequest&&window.XMLHttpRequest.prototype){var origOpen=window.XMLHttpRequest.prototype.open;window.XMLHttpRequest.prototype.open=function(m,u){try{u=rw(u);}catch(e){}return origOpen.apply(this,[m,u].concat(Array.prototype.slice.call(arguments,2)));};}var origInv=window.invokeNative;if(origInv){window.invokeNative=function(t,a){try{if(t==="openUrl"&&typeof a==="string"&&(a.indexOf("user-api-key")!==-1||a.indexOf("forum.cfx.re")!==-1||a.indexOf("portal.cfx.re")!==-1)){a=rw(a);}}catch(e){}return origInv.apply(this,arguments);};}})();</script>`

var htmlAssetCacheBustRe = regexp.MustCompile(`((?:src|href)=["'][^"'?]+\.(?:js|css))(?:\?v=nsm078)?(["'])`)

func patchUIBundleBytes(raw []byte) ([]byte, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, false, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	changed := false

	emailRegexVariants := [][]byte{
		[]byte(`/^[a-zA-Z0-9!#\$%&'*+\/=?\^_` + "`" + `{|}~\-]+(?:\.[a-zA-Z0-9!#\$%&'\*+\/=?\^_` + "`" + `{|}~\-]+)*@(?:[a-zA-Z0-9](?:[a-zA-Z0-9\-]*[a-zA-Z0-9])?\.)+[a-zA-Z0-9](?:[a-zA-Z0-9\-]*[a-zA-Z0-9])?$/`),
		[]byte(`/^[a-zA-Z0-9!#$%&'*+\/=?^_` + "`" + `{|}~\-]+(?:\.[a-zA-Z0-9!#$%&'*+\/=?^_` + "`" + `{|}~\-]+)*@(?:[a-zA-Z0-9](?:[a-zA-Z0-9\-]*[a-zA-Z0-9])?\.)+[a-zA-Z0-9](?:[a-zA-Z0-9\-]*[a-zA-Z0-9])?$/`),
		[]byte(`/^[a-zA-Z0-9!#$%&'*+\/=?^_` + "`" + `{|}~-]+(?:\.[a-zA-Z0-9!#$%&'*+\/=?^_` + "`" + `{|}~-]+)*@(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$/`),
		[]byte(`/^[^\s@]+@[^\s@]+\.[^\s@]+$/`),
	}

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
			// Point all Discord invite & forum replacements inside the UI bundle to the local NSM website
			body = bytes.ReplaceAll(body, []byte("https://discord.gg/UXj5QfCX88/terms"), []byte("http://127.0.0.1/docs"))
			body = bytes.ReplaceAll(body, []byte("https://discord.gg/UXj5QfCX88"), []byte("http://127.0.0.1"))
			body = bytes.ReplaceAll(body, []byte("https://forum.cfx.re"), []byte("http://127.0.0.1"))
			body = bytes.ReplaceAll(body, []byte("https://portal.cfx.re"), []byte("http://127.0.0.1/keymaster"))
			// Allow typing a username (e.g. "Cravenz") as well as an email in NSM.exe's in-app login input
			for _, pat := range emailRegexVariants {
				body = bytes.ReplaceAll(body, pat, []byte(`/^[^\s]{2,}$/`))
			}

			if strings.HasSuffix(lower, ".html") {
				// Upgrade or inject head interceptor into index.html (which NUISchemeHandler.cpp never caches)
				if bytes.Contains(body, []byte(uiDiscourseHeadInterceptorV1)) {
					body = bytes.ReplaceAll(body, []byte(uiDiscourseHeadInterceptorV1), []byte(uiDiscourseHeadInterceptor))
				} else if !bytes.Contains(body, []byte(`id="nsm-discourse-interceptor"`)) {
					if bytes.Contains(body, []byte("<head>")) {
						body = bytes.Replace(body, []byte("<head>"), []byte("<head>"+uiDiscourseHeadInterceptor), 1)
					} else {
						body = append([]byte(uiDiscourseHeadInterceptor), body...)
					}
				}
				// Append ?v=nsm078b cache-buster to static/js/main.js, mpMenu.js, and any .js/.css tags so CEF bypasses 30-day disk cache
				body = htmlAssetCacheBustRe.ReplaceAll(body, []byte("${1}?v=nsm078b${2}"))

				if bytes.Contains(body, []byte(uiClientAuthPollSnippetLegacy)) {
					body = bytes.ReplaceAll(body, []byte(uiClientAuthPollSnippetLegacy), []byte(uiClientAuthPollSnippet))
				} else if bytes.Contains(body, []byte(uiClientAuthPollSnippetV2)) {
					body = bytes.ReplaceAll(body, []byte(uiClientAuthPollSnippetV2), []byte(uiClientAuthPollSnippet))
				} else if !bytes.Contains(body, []byte(`id="nsm-client-auth-poll-v3"`)) {
					if bytes.Contains(body, []byte("</body>")) {
						body = bytes.Replace(body, []byte("</body>"), []byte(uiClientAuthPollSnippet+"</body>"), 1)
					} else {
						body = append(body, []byte(uiClientAuthPollSnippet)...)
					}
				}
			}
			if !bytes.Equal(body, orig) {
				changed = true
			}
		}

		hdr := zip.FileHeader{
			Name:     f.Name,
			Method:   zip.Deflate,
			Modified: f.Modified,
		}
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
	baseDirs := []string{"."}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" && dir != "." {
			baseDirs = append(baseDirs, dir)
		}
	}
	relCandidates := []string{
		"citizen/ui.zip",
		"citizen/ui-big.zip",
		"NSM.app/citizen/ui.zip",
		"NSM.app/citizen/ui-big.zip",
		"../citizen/ui.zip",
		"../citizen/ui-big.zip",
	}
	seen := map[string]bool{}
	patchedAny := false
	for _, b := range baseDirs {
		for _, rel := range relCandidates {
			p := filepath.Clean(filepath.Join(b, rel))
			if seen[p] {
				continue
			}
			seen[p] = true
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
					patchedAny = true
					log.Printf("[ui-patch] updated %s -> linked in-app login & portal to http://127.0.0.1", p)
				} else {
					_ = os.Remove(tmp)
				}
			}
		}
	}
	// Always clear pre-v078b CEF browser caches at least once (even if ui.zip came pre-patched in the release archive)
	for _, b := range baseDirs {
		for _, relRoot := range []string{"data/cache/browser", "NSM.app/data/cache/browser", "../data/cache/browser"} {
			root := filepath.Clean(filepath.Join(b, relRoot))
			st, err := os.Stat(root)
			if err != nil || !st.IsDir() {
				continue
			}
			sentinel := filepath.Join(root, ".nsm078b")
			if !patchedAny {
				if _, err := os.Stat(sentinel); err == nil {
					continue
				}
			}
			for _, sub := range []string{"Cache", "Code Cache"} {
				cd := filepath.Join(root, sub)
				if err := os.RemoveAll(cd); err == nil {
					log.Printf("[ui-patch] cleared CEF browser cache directory %s", cd)
				}
			}
			_ = os.WriteFile(sentinel, []byte("nsm078b\n"), 0644)
		}
	}
}
