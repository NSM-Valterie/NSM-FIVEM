// NSM accounts: users, sessions, roles (owner/admin/staff/user), upvotes.
// Storage: accounts.json next to the exe. Passwords: bcrypt.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Pass      string `json:"pass"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"createdAt"`
	LastLogin int64  `json:"lastLogin,omitempty"`
}

type Session struct {
	UserID  int64 `json:"userId"`
	Expires int64 `json:"expires"`
}

type Accounts struct {
	mu         sync.Mutex
	Users      []User             `json:"users"`
	Sessions   map[string]Session `json:"sessions"`
	Upvotes    map[string][]int64 `json:"upvotes"` // endpoint -> user ids
	ServerKeys []ServerKey        `json:"serverKeys,omitempty"`
	NextID     int64              `json:"nextId"`
}

var (
	accMu   sync.Mutex
	acc     *Accounts
	accPath = "accounts.json"
)

func loadAccounts() {
	accMu.Lock()
	defer accMu.Unlock()
	acc = &Accounts{Sessions: map[string]Session{}, Upvotes: map[string][]int64{}, NextID: 1}
	if b, err := os.ReadFile(accPath); err == nil {
		json.Unmarshal(b, acc)
	}
	if len(acc.Users) == 0 {
		// seed the owner
		acc.Users = append(acc.Users, User{ID: 1, Username: "Cravenz", Email: "cravenz@owner.local", Role: "owner", CreatedAt: time.Now().UnixMilli()})
		acc.NextID = 2
	}
	// ensure owner exists if the seed file lacked roles
	for i := range acc.Users {
		if acc.Users[i].Username == ownerName && acc.Users[i].Role == "" {
			acc.Users[i].Role = "owner"
		}
	}
	saveAccountsLocked()
}

func saveAccountsLocked() {
	b, _ := json.MarshalIndent(acc, "", "  ")
	tmp := accPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		log.Printf("accounts save error: %v", err)
		return
	}
	os.Rename(tmp, accPath)
}

func findUser(name string) *User {
	for i := range acc.Users {
		if strings.EqualFold(acc.Users[i].Username, name) {
			return &acc.Users[i]
		}
	}
	return nil
}

func userByID(id int64) *User {
	for i := range acc.Users {
		if acc.Users[i].ID == id {
			return &acc.Users[i]
		}
	}
	return nil
}

func newSession(userID int64) string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	acc.Sessions[tok] = Session{UserID: userID, Expires: time.Now().Add(30 * 24 * time.Hour).Unix()}
	return tok
}

func currentUser(r *http.Request) *User {
	tok := ""
	if c, err := r.Cookie("nsm_session"); err == nil && c.Value != "" {
		tok = c.Value
	} else if hdr := strings.TrimSpace(r.Header.Get("X-NSM-Session")); hdr != "" {
		tok = hdr
	}
	if tok == "" {
		return nil
	}
	accMu.Lock()
	defer accMu.Unlock()
	if acc == nil {
		return nil
	}
	s, ok := acc.Sessions[tok]
	if !ok || s.Expires < time.Now().Unix() {
		return nil
	}
	return userByID(s.UserID)
}

func roleAtLeast(role, min string) bool {
	rank := map[string]int{"user": 0, "staff": 1, "admin": 2, "owner": 3}
	return rank[role] >= rank[min]
}

// ---- JSON API for the client / external apps ----

func sendAccountJSON(w http.ResponseWriter, ok bool, msg string, u *User) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]interface{}{"ok": ok}
	if msg != "" {
		out["error"] = msg
	}
	if u != nil {
		out["user"] = map[string]interface{}{"id": u.ID, "username": u.Username, "email": u.Email, "role": u.Role}
	}
	json.NewEncoder(w).Encode(out)
}

func apiRegister(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password, Email string }
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		json.NewDecoder(r.Body).Decode(&req)
	} else {
		req.Username, req.Password, req.Email = r.FormValue("username"), r.FormValue("password"), r.FormValue("email")
	}
	un := strings.TrimSpace(req.Username)
	if len(un) < 3 || len(req.Password) < 6 {
		sendAccountJSON(w, false, "username needs 3+ chars, password 6+ chars", nil)
		return
	}
	accMu.Lock()
	defer accMu.Unlock()
	if findUser(un) != nil {
		sendAccountJSON(w, false, "that username is taken", nil)
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
	u := User{ID: acc.NextID, Username: un, Email: strings.TrimSpace(req.Email), Pass: string(h), Role: "user", CreatedAt: time.Now().UnixMilli()}
	acc.NextID++
	acc.Users = append(acc.Users, u)
	tok := newSession(u.ID)
	saveAccountsLocked()
	log.Printf("[accounts] registered %s (id %d)", un, u.ID)
	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	sendAccountJSON(w, true, "", &u)
}

func apiLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		json.NewDecoder(r.Body).Decode(&req)
	} else {
		req.Username, req.Password = r.FormValue("username"), r.FormValue("password")
	}
	accMu.Lock()
	defer accMu.Unlock()
	u := findUser(strings.TrimSpace(req.Username))
	if u == nil || bcrypt.CompareHashAndPassword([]byte(u.Pass), []byte(req.Password)) != nil {
		sendAccountJSON(w, false, "wrong username or password", nil)
		return
	}
	u.LastLogin = time.Now().UnixMilli()
	tok := newSession(u.ID)
	saveAccountsLocked()
	log.Printf("[accounts] login %s (role %s)", u.Username, u.Role)
	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	sendAccountJSON(w, true, "", u)
}

func apiLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("nsm_session"); err == nil {
		accMu.Lock()
		delete(acc.Sessions, c.Value)
		saveAccountsLocked()
		accMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: "", Path: "/", MaxAge: -1})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func apiMe(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	sendAccountJSON(w, u != nil, "", u)
}

func accountChangePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	old, nw := r.FormValue("old"), r.FormValue("new")
	accMu.Lock()
	defer accMu.Unlock()
	if bcrypt.CompareHashAndPassword([]byte(u.Pass), []byte(old)) != nil || len(nw) < 6 {
		renderPage(w, r, "account.tmpl", map[string]interface{}{"User": u, "Error": "old password wrong, or new password too short (6+)"})
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(nw), 10)
	u.Pass = string(h)
	saveAccountsLocked()
	renderPage(w, r, "account.tmpl", map[string]interface{}{"User": u, "Msg": "password updated"})
}

// ---- staff admin (owner session or existing bearer owner token) ----

func adminUsersPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || u.Role != "owner" {
		if !isOwner(r) {
			http.Error(w, "owner only", http.StatusForbidden)
			return
		}
	}
	accMu.Lock()
	users := append([]User{}, acc.Users...)
	accMu.Unlock()
	renderPage(w, r, "admin_users.tmpl", map[string]interface{}{"User": u, "Users": users, "Page": "/admin/users"})
}

func adminSetRole(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if (u == nil || u.Role != "owner") && !isOwner(r) {
		http.Error(w, "owner only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	role := r.FormValue("role")
	if !map[string]bool{"user": true, "staff": true, "admin": true, "owner": true}[role] {
		http.Error(w, "bad role", http.StatusBadRequest)
		return
	}
	accMu.Lock()
	defer accMu.Unlock()
	t := userByID(id)
	if t == nil || (t.Role == "owner" && u != nil && u.ID != t.ID) {
		http.Error(w, "cannot change that user", http.StatusBadRequest)
		return
	}
	if u != nil && u.ID == t.ID && role != "owner" {
		http.Error(w, "you cannot demote yourself", http.StatusBadRequest)
		return
	}
	t.Role = role
	saveAccountsLocked()
	log.Printf("[accounts] %s -> role %s (by %s)", t.Username, role, u.Username)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// ---- upvotes (one per account per server) ----

func upvoteServer(w http.ResponseWriter, r *http.Request, ep string) {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	accMu.Lock()
	defer accMu.Unlock()
	for _, id := range acc.Upvotes[ep] {
		if id == u.ID {
			http.Redirect(w, r, "/servers/"+ep, http.StatusSeeOther)
			return
		}
	}
	acc.Upvotes[ep] = append(acc.Upvotes[ep], u.ID)
	saveAccountsLocked()
	log.Printf("[upvote] %s upvoted %s", u.Username, ep)
	http.Redirect(w, r, "/servers/"+ep, http.StatusSeeOther)
}

func upvoteCount(ep string) int {
	accMu.Lock()
	defer accMu.Unlock()
	return len(acc.Upvotes[ep])
}

func setOwnerPassword(pw string) {
	accMu.Lock()
	defer accMu.Unlock()
	if u := findUser(ownerName); u != nil {
		h, _ := bcrypt.GenerateFromPassword([]byte(pw), 10)
		u.Pass = string(h)
		saveAccountsLocked()
		log.Printf("[accounts] owner password updated for %s", ownerName)
	}
}
