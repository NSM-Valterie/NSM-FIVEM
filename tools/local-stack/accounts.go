// NSM accounts: users, custom profiles, sessions, client tokens, roles (owner/admin/staff/user), upvotes.
// Storage: accounts.json next to the exe. Passwords: bcrypt.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID               int64  `json:"id"`
	Username         string `json:"username"`
	DisplayName      string `json:"displayName,omitempty"`
	Title            string `json:"title,omitempty"`
	Bio              string `json:"bio,omitempty"`
	Signature        string `json:"signature,omitempty"`
	AvatarURL        string `json:"avatarUrl,omitempty"`
	AvatarData       string `json:"avatarData,omitempty"` // data:image/...;base64,...
	AvatarColor      string `json:"avatarColor,omitempty"`
	BannerColor      string `json:"bannerColor,omitempty"`
	BannerURL        string `json:"bannerUrl,omitempty"`
	BannerData       string `json:"bannerData,omitempty"` // data:image/...;base64,... (supports animated GIF banners)
	ThemePrimary     string `json:"themePrimary,omitempty"`
	ThemeAccent      string `json:"themeAccent,omitempty"`
	AvatarDecoration string `json:"avatarDecoration,omitempty"` // cat_ears, sovereign_crown, cyber_visor, plasma_flames, volt_lightning, sakura_drift, singularity_orb, none
	ProfileEffect    string `json:"profileEffect,omitempty"`    // aurora_waves, cyber_matrix, starlight_warp, thunder_storm, solar_embers, holo_prism, none
	PulseTier        string `json:"pulseTier,omitempty"`        // core, pro, sovereign, eternal
	PulseSince       int64  `json:"pulseSince,omitempty"`
	IsPartner        bool   `json:"isPartner,omitempty"`
	SquadHouse       string `json:"squadHouse,omitempty"` // bravery, brilliance, balance, all
	Location         string `json:"location,omitempty"`
	Website          string `json:"website,omitempty"`
	DiscordTag       string `json:"discordTag,omitempty"`
	GitHub           string `json:"github,omitempty"`
	Steam            string `json:"steam,omitempty"`
	YouTube          string `json:"youtube,omitempty"`
	Twitch           string `json:"twitch,omitempty"`
	Twitter          string `json:"twitter,omitempty"`
	FavoriteServer   string `json:"favoriteServer,omitempty"`
	Email            string `json:"email"`
	Pass             string `json:"pass"`
	Role             string `json:"role"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt,omitempty"`
	LastLogin        int64  `json:"lastLogin,omitempty"`
}

func (u *User) IsFounderAccount() bool {
	if u == nil {
		return false
	}
	return strings.EqualFold(u.Username, ownerName) || strings.EqualFold(u.Username, "Cravenz") || u.Role == "owner" || u.ID == 1
}

func (u *User) EffectivePulseTier() string {
	if u == nil {
		return ""
	}
	if u.IsFounderAccount() {
		return "eternal"
	}
	switch strings.ToLower(strings.TrimSpace(u.PulseTier)) {
	case "eternal", "sovereign", "pro", "core":
		return strings.ToLower(strings.TrimSpace(u.PulseTier))
	default:
		return ""
	}
}

func (u *User) EffectivePulseTierLabel() string {
	switch u.EffectivePulseTier() {
	case "eternal":
		return "AFX Pulse Eternal (Founder Forever)"
	case "sovereign":
		return "AFX Pulse Sovereign"
	case "pro":
		return "AFX Pulse Pro"
	case "core":
		return "AFX Pulse Core"
	default:
		return "Standard Member"
	}
}

func (u *User) HasPulse() bool {
	return u != nil && u.EffectivePulseTier() != ""
}

func (u *User) HasPulseProOrHigher() bool {
	if u == nil {
		return false
	}
	t := u.EffectivePulseTier()
	return t == "pro" || t == "sovereign" || t == "eternal"
}

func (u *User) HasPartnerBadge() bool {
	if u == nil {
		return false
	}
	return u.IsPartner || u.Role == "partner" || u.IsFounderAccount()
}

func (u *User) EffectiveAvatarDecoration() string {
	if u == nil {
		return "none"
	}
	d := strings.ToLower(strings.TrimSpace(u.AvatarDecoration))
	switch d {
	case "cat_ears", "sovereign_crown", "cyber_visor", "plasma_flames", "volt_lightning", "sakura_drift", "singularity_orb", "none":
		return d
	}
	if u.IsFounderAccount() {
		return "cat_ears"
	}
	return "none"
}

func (u *User) EffectiveProfileEffect() string {
	if u == nil {
		return "none"
	}
	e := strings.ToLower(strings.TrimSpace(u.ProfileEffect))
	switch e {
	case "aurora_waves", "cyber_matrix", "starlight_warp", "thunder_storm", "solar_embers", "holo_prism", "none":
		return e
	}
	if u.IsFounderAccount() {
		return "aurora_waves"
	}
	return "none"
}

func (u *User) GitHubHandle() string {
	if u == nil {
		return ""
	}
	g := strings.TrimSpace(u.GitHub)
	g = strings.TrimPrefix(g, "https://github.com/")
	g = strings.TrimPrefix(g, "http://github.com/")
	g = strings.TrimPrefix(g, "github.com/")
	g = strings.TrimPrefix(g, "@")
	return strings.TrimSuffix(g, "/")
}

func (u *User) GitHubURL() string {
	if u == nil || strings.TrimSpace(u.GitHub) == "" {
		return ""
	}
	g := strings.TrimSpace(u.GitHub)
	if strings.HasPrefix(g, "http://") || strings.HasPrefix(g, "https://") {
		return g
	}
	return "https://github.com/" + u.GitHubHandle()
}

func (u *User) SteamURL() string {
	if u == nil || strings.TrimSpace(u.Steam) == "" {
		return ""
	}
	s := strings.TrimSpace(u.Steam)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "https://steamcommunity.com/id/" + url.PathEscape(s)
}

func (u *User) YouTubeURL() string {
	if u == nil || strings.TrimSpace(u.YouTube) == "" {
		return ""
	}
	y := strings.TrimSpace(u.YouTube)
	if strings.HasPrefix(y, "http://") || strings.HasPrefix(y, "https://") {
		return y
	}
	return "https://youtube.com/@" + strings.TrimPrefix(y, "@")
}

func (u *User) TwitchURL() string {
	if u == nil || strings.TrimSpace(u.Twitch) == "" {
		return ""
	}
	t := strings.TrimSpace(u.Twitch)
	if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
		return t
	}
	return "https://twitch.tv/" + strings.TrimPrefix(t, "@")
}

func (u *User) TwitterURL() string {
	if u == nil || strings.TrimSpace(u.Twitter) == "" {
		return ""
	}
	t := strings.TrimSpace(u.Twitter)
	if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
		return t
	}
	return "https://x.com/" + strings.TrimPrefix(t, "@")
}

func (u *User) EffectiveDisplayName() string {
	if u == nil {
		return ""
	}
	if strings.TrimSpace(u.DisplayName) != "" {
		return u.DisplayName
	}
	return u.Username
}

func (u *User) EffectiveTitle() string {
	if u == nil {
		return ""
	}
	if strings.TrimSpace(u.Title) != "" {
		return u.Title
	}
	switch u.Role {
	case "owner":
		return "NSM Founder & Owner"
	case "admin":
		return "NSM Administrator"
	case "staff":
		return "NSM Staff"
	case "partner":
		return "AFX Partnered Server Owner"
	default:
		if u.IsPartner {
			return "AFX Partnered Server Owner"
		}
		return "NSM Community Member"
	}
}

func isValidUsername(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func isValidHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func hexToRGBString(hexStr string, defR, defG, defB int) string {
	defaultStr := strconv.Itoa(defR) + ", " + strconv.Itoa(defG) + ", " + strconv.Itoa(defB)
	if !isValidHexColor(hexStr) {
		return defaultStr
	}
	r, err1 := strconv.ParseInt(hexStr[1:3], 16, 32)
	g, err2 := strconv.ParseInt(hexStr[3:5], 16, 32)
	b, err3 := strconv.ParseInt(hexStr[5:7], 16, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		return defaultStr
	}
	return strconv.FormatInt(r, 10) + ", " + strconv.FormatInt(g, 10) + ", " + strconv.FormatInt(b, 10)
}

func (u *User) EffectiveAvatarColor() string {
	if u != nil && isValidHexColor(u.AvatarColor) {
		return u.AvatarColor
	}
	palette := []string{"#2F7BFF", "#07baae", "#e7064f", "#8b5cf6", "#f59e0b", "#10b981"}
	if u == nil || u.ID <= 0 {
		return palette[0]
	}
	return palette[int(u.ID)%len(palette)]
}

func (u *User) EffectiveBannerColor() string {
	if u != nil && isValidHexColor(u.BannerColor) {
		return u.BannerColor
	}
	if u != nil && isValidHexColor(u.ThemePrimary) {
		return u.ThemePrimary
	}
	return "#1e293b"
}

func (u *User) EffectiveThemePrimary() string {
	if u != nil && isValidHexColor(u.ThemePrimary) {
		return u.ThemePrimary
	}
	if u != nil && isValidHexColor(u.BannerColor) {
		return u.BannerColor
	}
	return "#1d3557"
}

func (u *User) EffectiveThemeAccent() string {
	if u != nil && isValidHexColor(u.ThemeAccent) {
		return u.ThemeAccent
	}
	if u != nil && isValidHexColor(u.AvatarColor) {
		return u.AvatarColor
	}
	return "#2F7BFF"
}

func (u *User) ThemePrimaryRGB() string {
	return hexToRGBString(u.EffectiveThemePrimary(), 29, 53, 87)
}

func (u *User) ThemeAccentRGB() string {
	return hexToRGBString(u.EffectiveThemeAccent(), 47, 123, 255)
}

func (u *User) BannerColorRGB() string {
	return hexToRGBString(u.EffectiveBannerColor(), 30, 41, 59)
}

func (u *User) AvatarColorRGB() string {
	return hexToRGBString(u.EffectiveAvatarColor(), 47, 123, 255)
}

func (u *User) ThemeStyleAttr() template.CSS {
	return template.CSS(
		"--prof-primary: " + u.EffectiveThemePrimary() +
			"; --prof-accent: " + u.EffectiveThemeAccent() +
			"; --prof-primary-rgb: " + u.ThemePrimaryRGB() +
			"; --prof-accent-rgb: " + u.ThemeAccentRGB() +
			"; --prof-banner: " + u.EffectiveBannerColor() +
			"; --prof-avatar: " + u.EffectiveAvatarColor() + ";",
	)
}

func (u *User) BannerPath() string {
	if u == nil {
		return ""
	}
	if strings.HasPrefix(u.BannerData, "data:image/") {
		v := u.UpdatedAt
		if v == 0 {
			v = u.CreatedAt
		}
		return "/banners/u/" + url.PathEscape(u.Username) + "?v=" + strconv.FormatInt(v, 10)
	}
	if u.BannerURL != "" && !strings.ContainsAny(u.BannerURL, `"'<>()\`) {
		return u.BannerURL
	}
	return ""
}

func (u *User) BannerStyleAttr() template.CSS {
	base := "background: linear-gradient(135deg, var(--prof-banner) 0%, var(--prof-primary) 52%, var(--prof-accent) 100%), url('/img/nsm-backdrop.png') center/256px repeat;"
	if bp := u.BannerPath(); bp != "" {
		base += "background-image: linear-gradient(180deg, rgba(14,16,22,0.18), rgba(14,16,22,0.82)), url('" + bp + "');background-size:cover;background-position:center;"
	}
	return template.CSS(base)
}

func (u *User) AvatarRingStyleAttr() template.CSS {
	return template.CSS("border-radius:50%;border:2px solid " + u.EffectiveThemePrimary() + ";")
}

func (u *User) AvatarPath() string {
	if u == nil {
		return "/avatars/u/guest/120.png"
	}
	v := u.UpdatedAt
	if v == 0 {
		v = u.CreatedAt
	}
	return "/avatars/u/" + url.PathEscape(u.Username) + "/120.png?v=" + strconv.FormatInt(v, 10)
}

func (u *User) AvatarTemplate() string {
	if u == nil {
		return "/avatars/u/guest/{size}.png"
	}
	v := u.UpdatedAt
	if v == 0 {
		v = u.CreatedAt
	}
	return "/avatars/u/" + url.PathEscape(u.Username) + "/{size}.png?v=" + strconv.FormatInt(v, 10)
}

type Session struct {
	UserID  int64 `json:"userId"`
	Expires int64 `json:"expires"`
}

type ClientToken struct {
	Token     string `json:"token"`
	UserID    int64  `json:"userId"`
	ClientID  string `json:"clientId"`
	AppName   string `json:"appName,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed,omitempty"`
}

type Accounts struct {
	mu           sync.Mutex
	Users        []User                 `json:"users"`
	Sessions     map[string]Session     `json:"sessions"`
	ClientTokens map[string]ClientToken `json:"clientTokens,omitempty"`
	Upvotes      map[string][]int64     `json:"upvotes"` // endpoint -> user ids
	ServerKeys   []ServerKey            `json:"serverKeys,omitempty"`
	NextID       int64                  `json:"nextId"`
}

var (
	accMu   sync.Mutex
	acc     *Accounts
	accPath = "accounts.json"
)

func loadAccounts() {
	accMu.Lock()
	defer accMu.Unlock()
	acc = &Accounts{
		Sessions:     map[string]Session{},
		ClientTokens: map[string]ClientToken{},
		Upvotes:      map[string][]int64{},
		NextID:       1,
	}
	if b, err := os.ReadFile(accPath); err == nil {
		json.Unmarshal(b, acc)
	}
	if acc.Sessions == nil {
		acc.Sessions = map[string]Session{}
	}
	if acc.ClientTokens == nil {
		acc.ClientTokens = map[string]ClientToken{}
	}
	if acc.Upvotes == nil {
		acc.Upvotes = map[string][]int64{}
	}
	if len(acc.Users) == 0 {
		acc.Users = append(acc.Users, User{
			ID:               1,
			Username:         "Cravenz",
			DisplayName:      "Cravenz",
			Title:            "NSM Founder",
			Bio:              "Founder & Lead Developer of the NSM Multiplayer Platform.",
			Signature:        "NSM - Powered by AFX | Official Platform Founder",
			AvatarColor:      "#2F7BFF",
			BannerColor:      "#1d3557",
			ThemePrimary:     "#1d3557",
			ThemeAccent:      "#2F7BFF",
			AvatarDecoration: "cat_ears",
			ProfileEffect:    "aurora_waves",
			PulseTier:        "eternal",
			PulseSince:       time.Now().UnixMilli(),
			IsPartner:        true,
			SquadHouse:       "all",
			Location:         "United States",
			DiscordTag:       "cravenz",
			GitHub:           "platinum-cfx",
			Website:          "https://github.com/platinum-cfx/NSM-FIVEM",
			FavoriteServer:   "NSM Official Server",
			Email:            "cravenz@owner.local",
			Role:             "owner",
			CreatedAt:        time.Now().UnixMilli(),
		})
		acc.NextID = 2
	}
	for i := range acc.Users {
		if strings.EqualFold(acc.Users[i].Username, ownerName) || strings.EqualFold(acc.Users[i].Username, "Cravenz") {
			if acc.Users[i].Role == "" {
				acc.Users[i].Role = "owner"
			}
			acc.Users[i].PulseTier = "eternal"
			acc.Users[i].IsPartner = true
			if acc.Users[i].AvatarDecoration == "" {
				acc.Users[i].AvatarDecoration = "cat_ears"
			}
			if acc.Users[i].ProfileEffect == "" {
				acc.Users[i].ProfileEffect = "aurora_waves"
			}
			if acc.Users[i].SquadHouse == "" {
				acc.Users[i].SquadHouse = "all"
			}
			if acc.Users[i].DiscordTag == "" {
				acc.Users[i].DiscordTag = "cravenz"
			}
			if acc.Users[i].GitHub == "" {
				acc.Users[i].GitHub = "platinum-cfx"
			}
		}
		if acc.Users[i].ID >= acc.NextID {
			acc.NextID = acc.Users[i].ID + 1
		}
	}
	saveAccountsLocked()
}

func saveAccountsLocked() {
	b, _ := json.MarshalIndent(acc, "", "  ")
	tmp := accPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		log.Printf("accounts save error: %v", err)
		_ = os.WriteFile(accPath, b, 0600)
		return
	}
	if err := os.Rename(tmp, accPath); err != nil {
		_ = os.WriteFile(accPath, b, 0600)
		_ = os.Remove(tmp)
	}
}

func findUser(name string) *User {
	name = strings.TrimSpace(name)
	for i := range acc.Users {
		if strings.EqualFold(acc.Users[i].Username, name) {
			return &acc.Users[i]
		}
	}
	return nil
}

func findUserByLoginOrEmail(ident string) *User {
	ident = strings.TrimSpace(ident)
	if ident == "" {
		return nil
	}
	if u := findUser(ident); u != nil {
		return u
	}
	for i := range acc.Users {
		if acc.Users[i].Email != "" && strings.EqualFold(acc.Users[i].Email, ident) {
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

func wantsJSONResponse(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "application/json") {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		return true
	}
	return false
}

func safeRedirectTarget(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/account"
	}
	return next
}

// ---- JSON API & HTML Form Handlers for Login / Register ----

func userPublicMap(u *User) map[string]interface{} {
	if u == nil {
		return nil
	}
	return map[string]interface{}{
		"id":             u.ID,
		"username":       u.Username,
		"displayName":    u.EffectiveDisplayName(),
		"title":          u.EffectiveTitle(),
		"bio":            u.Bio,
		"avatarUrl":      u.AvatarPath(),
		"avatarTemplate": u.AvatarTemplate(),
		"avatarColor":    u.EffectiveAvatarColor(),
		"bannerColor":    u.EffectiveBannerColor(),
		"bannerUrl":      u.BannerURL,
		"location":       u.Location,
		"website":        u.Website,
		"discordTag":     u.DiscordTag,
		"favoriteServer": u.FavoriteServer,
		"email":          u.Email,
		"role":           u.Role,
		"createdAt":      u.CreatedAt,
	}
}

func sendAccountJSON(w http.ResponseWriter, ok bool, msg string, u *User) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]interface{}{"ok": ok}
	if msg != "" {
		out["error"] = msg
	}
	if u != nil {
		out["user"] = userPublicMap(u)
	}
	json.NewEncoder(w).Encode(out)
}

func apiRegister(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password, Email, Next string }
	isJSON := wantsJSONResponse(r)
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") {
		json.NewDecoder(r.Body).Decode(&req)
	} else {
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
		req.Email = r.FormValue("email")
		req.Next = r.FormValue("next")
	}
	un := strings.TrimSpace(req.Username)
	if len(un) < 3 || len(req.Password) < 6 {
		msg := "Username needs 3+ characters and password needs 6+ characters."
		if isJSON {
			sendAccountJSON(w, false, "username needs 3+ chars, password 6+ chars", nil)
			return
		}
		renderPage(w, r, "register.tmpl", map[string]interface{}{"Title": "Create account", "Error": msg, "Next": req.Next, "PrefillUser": un, "PrefillEmail": req.Email})
		return
	}
	accMu.Lock()
	if existing := findUser(un); existing != nil {
		// If the seeded owner account (Cravenz) has not had a password set yet, allow claiming it via Register too.
		if existing.Pass == "" && len(req.Password) >= 6 {
			h, _ := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
			existing.Pass = string(h)
			if strings.TrimSpace(req.Email) != "" {
				existing.Email = strings.TrimSpace(req.Email)
			}
			now := time.Now().UnixMilli()
			existing.UpdatedAt = now
			existing.LastLogin = now
			tok := newSession(existing.ID)
			userCopy := *existing
			saveAccountsLocked()
			accMu.Unlock()
			log.Printf("[accounts] claimed seeded account %s (role %s)", userCopy.Username, userCopy.Role)
			http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
			recordWebsiteLoginForClientAuth(userCopy.ID)
			if isJSON {
				sendAccountJSON(w, true, "", &userCopy)
				return
			}
			http.Redirect(w, r, safeRedirectTarget(req.Next), http.StatusSeeOther)
			return
		}
		accMu.Unlock()
		if isJSON {
			sendAccountJSON(w, false, "that username is taken", nil)
			return
		}
		renderPage(w, r, "register.tmpl", map[string]interface{}{"Title": "Create account", "Error": "That username is already taken.", "Next": req.Next, "PrefillEmail": req.Email})
		return
	}
	role := "user"
	// If this is the very first registered user with a password or matches ownerName, grant owner if none has password yet
	if strings.EqualFold(un, ownerName) {
		role = "owner"
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
	now := time.Now().UnixMilli()
	u := User{
		ID:          acc.NextID,
		Username:    un,
		DisplayName: un,
		Email:       strings.TrimSpace(req.Email),
		Pass:        string(h),
		Role:        role,
		CreatedAt:   now,
		UpdatedAt:   now,
		LastLogin:   now,
	}
	acc.NextID++
	acc.Users = append(acc.Users, u)
	tok := newSession(u.ID)
	saveAccountsLocked()
	accMu.Unlock()

	log.Printf("[accounts] registered %s (id %d)", un, u.ID)
	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	recordWebsiteLoginForClientAuth(u.ID)

	if isJSON {
		sendAccountJSON(w, true, "", &u)
		return
	}
	http.Redirect(w, r, safeRedirectTarget(req.Next), http.StatusSeeOther)
}

func apiLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password, Next string }
	isJSON := wantsJSONResponse(r)
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") {
		json.NewDecoder(r.Body).Decode(&req)
	} else {
		req.Username = r.FormValue("username")
		if req.Username == "" {
			req.Username = r.FormValue("login")
		}
		req.Password = r.FormValue("password")
		req.Next = r.FormValue("next")
	}
	accMu.Lock()
	u := findUserByLoginOrEmail(strings.TrimSpace(req.Username))
	if u != nil && u.Pass == "" && len(req.Password) >= 1 {
		// Seeded account (e.g. Cravenz owner) had no password set yet: initialize it on first login.
		h, _ := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
		u.Pass = string(h)
		u.UpdatedAt = time.Now().UnixMilli()
		log.Printf("[accounts] initialized password on first login for %s (role %s)", u.Username, u.Role)
	}
	if u == nil || bcrypt.CompareHashAndPassword([]byte(u.Pass), []byte(req.Password)) != nil {
		accMu.Unlock()
		if isJSON {
			sendAccountJSON(w, false, "wrong username or password", nil)
			return
		}
		renderPage(w, r, "login.tmpl", map[string]interface{}{"Title": "Log in", "Error": "Wrong username or password.", "Next": req.Next, "PrefillUser": req.Username})
		return
	}
	u.LastLogin = time.Now().UnixMilli()
	tok := newSession(u.ID)
	userCopy := *u
	saveAccountsLocked()
	accMu.Unlock()

	log.Printf("[accounts] login %s (role %s)", userCopy.Username, userCopy.Role)
	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	recordWebsiteLoginForClientAuth(userCopy.ID)

	if isJSON {
		sendAccountJSON(w, true, "", &userCopy)
		return
	}
	http.Redirect(w, r, safeRedirectTarget(req.Next), http.StatusSeeOther)
}

func isLoopbackClient(r *http.Request) bool {
	ip := clientIP(r)
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost" || strings.HasPrefix(ip, "127.")
}

// accountClaimOwner allows the local machine owner on 127.0.0.1 to claim or reset the Cravenz owner account password and sign in immediately.
func accountClaimOwner(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackClient(r) {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	pw := r.FormValue("password")
	next := r.FormValue("next")
	if len(pw) > 0 && len(pw) < 6 {
		renderPage(w, r, "login.tmpl", map[string]interface{}{
			"Title": "Log in",
			"Error": "Owner password must be at least 6 characters (or leave blank if already set).",
			"Next":  next,
		})
		return
	}

	accMu.Lock()
	u := findUser(ownerName)
	now := time.Now().UnixMilli()
	if u == nil {
		acc.Users = append(acc.Users, User{
			ID:          acc.NextID,
			Username:    ownerName,
			DisplayName: ownerName,
			Title:       "NSM Founder & Owner",
			Bio:         "Founder & Lead Developer of the NSM Multiplayer Platform.",
			AvatarColor: "#2F7BFF",
			BannerColor: "#1d3557",
			Email:       "cravenz@owner.local",
			Role:        "owner",
			CreatedAt:   now,
			UpdatedAt:   now,
			LastLogin:   now,
		})
		acc.NextID++
		u = findUser(ownerName)
	}
	u.Role = "owner"
	if len(pw) >= 6 {
		h, _ := bcrypt.GenerateFromPassword([]byte(pw), 10)
		u.Pass = string(h)
		u.UpdatedAt = now
	}
	u.LastLogin = now
	tok := newSession(u.ID)
	userCopy := *u
	saveAccountsLocked()
	accMu.Unlock()

	log.Printf("[accounts] local owner claim/sign-in for %s (id %d)", userCopy.Username, userCopy.ID)
	http.SetCookie(w, &http.Cookie{Name: "nsm_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	recordWebsiteLoginForClientAuth(userCopy.ID)

	if wantsJSONResponse(r) {
		sendAccountJSON(w, true, "", &userCopy)
		return
	}
	http.Redirect(w, r, safeRedirectTarget(next), http.StatusSeeOther)
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
	target := userByID(u.ID)
	if target == nil || bcrypt.CompareHashAndPassword([]byte(target.Pass), []byte(old)) != nil || len(nw) < 6 {
		accMu.Unlock()
		renderAccountPageWithMsg(w, r, u, "", "Current password is wrong, or new password is shorter than 6 characters.")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(nw), 10)
	target.Pass = string(h)
	target.UpdatedAt = time.Now().UnixMilli()
	userCopy := *target
	saveAccountsLocked()
	accMu.Unlock()
	renderAccountPageWithMsg(w, r, &userCopy, "Password updated successfully.", "")
}

func accountUpdateProfile(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		if wantsJSONResponse(r) {
			sendAccountJSON(w, false, "login required", nil)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		_ = r.ParseMultipartForm(4 << 20)
	} else {
		_ = r.ParseForm()
	}

	firstNonEmpty := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(r.FormValue(k)); v != "" {
				return v
			}
		}
		return ""
	}
	newUsername := firstNonEmpty("new_username", "username")
	displayName := firstNonEmpty("display_name", "displayName")
	title := firstNonEmpty("title")
	bio := firstNonEmpty("bio")
	signature := firstNonEmpty("signature")
	avatarURL := firstNonEmpty("avatar_url", "avatarUrl")
	avatarColor := firstNonEmpty("avatar_color", "avatarColor")
	bannerColor := firstNonEmpty("banner_color", "bannerColor")
	bannerURL := firstNonEmpty("banner_url", "bannerUrl")
	themePrimary := firstNonEmpty("theme_primary", "themePrimary")
	themeAccent := firstNonEmpty("theme_accent", "themeAccent")
	avatarDecoration := firstNonEmpty("avatar_decoration", "avatarDecoration")
	profileEffect := firstNonEmpty("profile_effect", "profileEffect")
	squadHouse := firstNonEmpty("squad_house", "squadHouse")
	location := firstNonEmpty("location")
	website := firstNonEmpty("website")
	discordTag := firstNonEmpty("discord_tag", "discordTag", "discord")
	github := firstNonEmpty("github", "github_url", "githubUrl")
	steam := firstNonEmpty("steam", "steam_id", "steamId")
	youtube := firstNonEmpty("youtube")
	twitch := firstNonEmpty("twitch")
	twitter := firstNonEmpty("twitter", "x_handle")
	favServer := firstNonEmpty("favorite_server", "favoriteServer")
	email := firstNonEmpty("email")
	clearAvatar := r.FormValue("clear_avatar") == "1" || r.FormValue("clearAvatar") == "1"
	clearBanner := r.FormValue("clear_banner") == "1" || r.FormValue("clearBanner") == "1"

	if len(displayName) > 48 {
		displayName = displayName[:48]
	}
	if len(title) > 64 {
		title = title[:64]
	}
	if len(bio) > 600 {
		bio = bio[:600]
	}
	if len(signature) > 200 {
		signature = signature[:200]
	}

	var uploadedAvatarData, uploadedBannerData string
	if r.MultipartForm != nil {
		files := r.MultipartForm.File["avatar_file"]
		if len(files) == 0 {
			files = r.MultipartForm.File["avatarFile"]
		}
		if len(files) > 0 {
			f, err := files[0].Open()
			if err == nil {
				defer f.Close()
				raw, err := io.ReadAll(io.LimitReader(f, 3<<20))
				if err == nil && len(raw) > 0 {
					ct := http.DetectContentType(raw)
					if ct == "image/png" || ct == "image/jpeg" || ct == "image/gif" || ct == "image/webp" {
						uploadedAvatarData = "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(raw)
					}
				}
			}
		}
		bfiles := r.MultipartForm.File["banner_file"]
		if len(bfiles) == 0 {
			bfiles = r.MultipartForm.File["bannerFile"]
		}
		if len(bfiles) > 0 {
			bf, err := bfiles[0].Open()
			if err == nil {
				defer bf.Close()
				raw, err := io.ReadAll(io.LimitReader(bf, 4<<20))
				if err == nil && len(raw) > 0 {
					ct := http.DetectContentType(raw)
					if ct == "image/png" || ct == "image/jpeg" || ct == "image/gif" || ct == "image/webp" {
						uploadedBannerData = "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(raw)
					}
				}
			}
		}
	}

	accMu.Lock()
	target := userByID(u.ID)
	if target == nil {
		accMu.Unlock()
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var oldUsername, updatedUsername string
	if newUsername != "" && newUsername != target.Username && isValidUsername(newUsername) {
		existing := findUser(newUsername)
		if existing == nil || existing.ID == target.ID {
			oldUsername = target.Username
			updatedUsername = newUsername
			if strings.EqualFold(target.Username, ownerName) {
				ownerName = newUsername
			}
			target.Username = newUsername
		}
	}
	target.DisplayName = displayName
	target.Title = title
	target.Bio = bio
	if signature != "" || r.Form.Has("signature") {
		target.Signature = signature
	}
	if clearAvatar {
		target.AvatarData = ""
		target.AvatarURL = ""
	} else {
		if uploadedAvatarData != "" {
			target.AvatarData = uploadedAvatarData
		}
		if avatarURL != "" || r.Form.Has("avatarUrl") || r.Form.Has("avatar_url") {
			target.AvatarURL = avatarURL
		}
	}
	if clearBanner {
		target.BannerData = ""
		target.BannerURL = ""
	} else {
		if uploadedBannerData != "" {
			target.BannerData = uploadedBannerData
		}
		if bannerURL != "" || r.Form.Has("bannerUrl") || r.Form.Has("banner_url") {
			target.BannerURL = bannerURL
		}
	}
	if isValidHexColor(avatarColor) {
		target.AvatarColor = avatarColor
		if !isValidHexColor(themeAccent) && target.ThemeAccent == "" {
			target.ThemeAccent = avatarColor
		}
	}
	if isValidHexColor(bannerColor) {
		target.BannerColor = bannerColor
		if !isValidHexColor(themePrimary) && target.ThemePrimary == "" {
			target.ThemePrimary = bannerColor
		}
	}
	if isValidHexColor(themePrimary) {
		target.ThemePrimary = themePrimary
		if !isValidHexColor(bannerColor) {
			target.BannerColor = themePrimary
		}
	}
	if isValidHexColor(themeAccent) {
		target.ThemeAccent = themeAccent
	}
	if avatarDecoration != "" {
		target.AvatarDecoration = strings.ToLower(avatarDecoration)
	}
	if profileEffect != "" {
		target.ProfileEffect = strings.ToLower(profileEffect)
	}
	if squadHouse != "" {
		sh := strings.ToLower(squadHouse)
		if sh == "bravery" || sh == "brilliance" || sh == "balance" || sh == "all" {
			target.SquadHouse = sh
		}
	}
	if target.IsFounderAccount() {
		target.PulseTier = "eternal"
		target.IsPartner = true
	}
	target.Location = location
	target.Website = website
	target.DiscordTag = discordTag
	if github != "" || r.Form.Has("github") {
		target.GitHub = github
	}
	if steam != "" || r.Form.Has("steam") {
		target.Steam = steam
	}
	if youtube != "" || r.Form.Has("youtube") {
		target.YouTube = youtube
	}
	if twitch != "" || r.Form.Has("twitch") {
		target.Twitch = twitch
	}
	if twitter != "" || r.Form.Has("twitter") {
		target.Twitter = twitter
	}
	target.FavoriteServer = favServer
	if email != "" {
		target.Email = email
	}
	target.UpdatedAt = time.Now().UnixMilli()
	userCopy := *target
	saveAccountsLocked()
	accMu.Unlock()

	if oldUsername != "" && updatedUsername != "" {
		forumMu.Lock()
		if forum != nil {
			changedForum := false
			for ti := range forum.Topics {
				if strings.EqualFold(forum.Topics[ti].AuthorUsername, oldUsername) {
					forum.Topics[ti].AuthorUsername = updatedUsername
					changedForum = true
				}
				for pi := range forum.Topics[ti].Posts {
					if strings.EqualFold(forum.Topics[ti].Posts[pi].AuthorUsername, oldUsername) {
						forum.Topics[ti].Posts[pi].AuthorUsername = updatedUsername
						changedForum = true
					}
					for li := range forum.Topics[ti].Posts[pi].Likes {
						if strings.EqualFold(forum.Topics[ti].Posts[pi].Likes[li], oldUsername) {
							forum.Topics[ti].Posts[pi].Likes[li] = updatedUsername
							changedForum = true
						}
					}
				}
			}
			if changedForum {
				saveForumLocked()
			}
		}
		forumMu.Unlock()
	}

	if wantsJSONResponse(r) {
		sendAccountJSON(w, true, "", &userCopy)
		return
	}
	returnTab := strings.ToLower(strings.TrimSpace(r.FormValue("return_tab")))
	if returnTab == "" {
		returnTab = "preferences"
	}
	data := buildDiscourseProfileData(&userCopy, &userCopy, returnTab, "Your profile, display name, decorations, and layout theme colors have been updated.", "")
	renderPage(w, r, "profile.tmpl", data)
}

func accountSubscribePulse(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		if wantsJSONResponse(r) {
			sendAccountJSON(w, false, "login required", nil)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	tier := strings.ToLower(strings.TrimSpace(r.FormValue("tier")))
	if tier != "core" && tier != "pro" && tier != "sovereign" && tier != "eternal" {
		tier = "pro"
	}

	accMu.Lock()
	target := userByID(u.ID)
	if target == nil {
		accMu.Unlock()
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if target.IsFounderAccount() {
		target.PulseTier = "eternal"
	} else {
		target.PulseTier = tier
	}
	if target.PulseSince == 0 {
		target.PulseSince = time.Now().UnixMilli()
	}
	if target.AvatarDecoration == "" || target.AvatarDecoration == "none" {
		target.AvatarDecoration = "cat_ears"
	}
	if (tier == "pro" || tier == "sovereign" || target.IsFounderAccount()) && (target.ProfileEffect == "" || target.ProfileEffect == "none") {
		target.ProfileEffect = "aurora_waves"
	}
	target.UpdatedAt = time.Now().UnixMilli()
	userCopy := *target
	saveAccountsLocked()
	accMu.Unlock()

	if wantsJSONResponse(r) {
		sendAccountJSON(w, true, "", &userCopy)
		return
	}
	msg := "AFX Pulse (" + userCopy.EffectivePulseTierLabel() + ") is now active! Animated GIFs, Full-Page Layout Colors, Cat Ears, Moving Effects, and the AFX Pulse Badge are unlocked."
	data := buildDiscourseProfileData(&userCopy, &userCopy, "pulse", msg, "")
	renderPage(w, r, "profile.tmpl", data)
}

func serveUserBanner(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	accMu.Lock()
	u := findUser(username)
	var bannerData, bannerURL string
	if u != nil {
		bannerData = u.BannerData
		bannerURL = u.BannerURL
	}
	accMu.Unlock()

	if strings.HasPrefix(bannerData, "data:image/") {
		if idx := strings.Index(bannerData, ";base64,"); idx > 0 {
			ct := strings.TrimPrefix(bannerData[:idx], "data:")
			if raw, err := base64.StdEncoding.DecodeString(bannerData[idx+8:]); err == nil && len(raw) > 0 {
				w.Header().Set("Content-Type", ct)
				w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
				w.WriteHeader(http.StatusOK)
				w.Write(raw)
				return
			}
		}
	}
	if strings.HasPrefix(bannerURL, "http://") || strings.HasPrefix(bannerURL, "https://") {
		http.Redirect(w, r, bannerURL, http.StatusFound)
		return
	}
	http.NotFound(w, r)
}

func renderAccountPageWithMsg(w http.ResponseWriter, r *http.Request, u *User, msg, errMsg string) {
	if u == nil {
		u = currentUser(r)
	}
	if u != nil {
		data := buildDiscourseProfileData(u, u, "preferences", msg, errMsg)
		renderPage(w, r, "profile.tmpl", data)
		return
	}
	renderPage(w, r, "account.tmpl", map[string]interface{}{
		"Title":      "My Account & Profile",
		"User":       nil,
		"Msg":        msg,
		"Error":      errMsg,
		"HasPending": hasPendingClientAuth(),
	})
}

// ---- Avatar PNG endpoint (/avatars/u/{username}/{size}) ----

// 5x7 bitmap font for uppercase A-Z and 0-9 so generated avatars look crisp in both browser and NSM client.
var avatarGlyphs = map[byte][7]uint8{
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'B': {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	'C': {0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E},
	'D': {0x1C, 0x12, 0x11, 0x11, 0x11, 0x12, 0x1C},
	'E': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	'F': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	'G': {0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F},
	'H': {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'I': {0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'J': {0x07, 0x02, 0x02, 0x02, 0x12, 0x12, 0x0C},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	'M': {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	'Q': {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	'R': {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	'S': {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E},
	'T': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11},
	'X': {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
	'0': {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	'1': {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'2': {0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F},
	'3': {0x1F, 0x02, 0x04, 0x06, 0x01, 0x11, 0x0E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	'6': {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
}

func parseHexColor(s string) color.RGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 6 {
		if b, err := hex.DecodeString(s); err == nil && len(b) == 3 {
			return color.RGBA{R: b[0], G: b[1], B: b[2], A: 255}
		}
	}
	return color.RGBA{R: 47, G: 123, B: 255, A: 255}
}

func generateInitialsPNG(initial byte, bgHex string, size int) []byte {
	if size < 32 {
		size = 64
	}
	if size > 512 {
		size = 256
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := parseHexColor(bgHex)
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	if initial >= 'a' && initial <= 'z' {
		initial -= 32
	}
	glyph, ok := avatarGlyphs[initial]
	if !ok {
		glyph = avatarGlyphs['N']
	}
	scale := size / 14
	if scale < 1 {
		scale = 1
	}
	gw, gh := 5*scale, 7*scale
	ox, oy := (size-gw)/2, (size-gh)/2
	fg := color.RGBA{R: 255, G: 255, B: 245, A: 255}
	for row := 0; row < 7; row++ {
		bits := glyph[row]
		for col := 0; col < 5; col++ {
			if (bits>>(4-col))&1 == 1 {
				rect := image.Rect(ox+col*scale, oy+row*scale, ox+(col+1)*scale, oy+(row+1)*scale)
				draw.Draw(img, rect, &image.Uniform{C: fg}, image.Point{}, draw.Src)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func serveUserAvatar(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	sizeStr := strings.TrimSuffix(r.PathValue("size"), ".png")
	size, _ := strconv.Atoi(sizeStr)
	if size <= 0 {
		size = 120
	}

	accMu.Lock()
	u := findUser(username)
	var avatarData, avatarURL, bgHex string
	initial := byte('N')
	if len(username) > 0 {
		initial = username[0]
	}
	if u != nil {
		avatarData = u.AvatarData
		avatarURL = u.AvatarURL
		bgHex = u.EffectiveAvatarColor()
		dn := u.EffectiveDisplayName()
		if len(dn) > 0 {
			initial = dn[0]
		}
	} else {
		bgHex = "#2F7BFF"
	}
	accMu.Unlock()

	if strings.HasPrefix(avatarData, "data:image/") {
		if idx := strings.Index(avatarData, ";base64,"); idx > 0 {
			ct := strings.TrimPrefix(avatarData[:idx], "data:")
			if raw, err := base64.StdEncoding.DecodeString(avatarData[idx+8:]); err == nil && len(raw) > 0 {
				w.Header().Set("Content-Type", ct)
				w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
				w.WriteHeader(http.StatusOK)
				w.Write(raw)
				return
			}
		}
	}
	if strings.HasPrefix(avatarURL, "http://") || strings.HasPrefix(avatarURL, "https://") {
		http.Redirect(w, r, avatarURL, http.StatusFound)
		return
	}

	pngBytes := generateInitialsPNG(initial, bgHex, size)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(pngBytes)))
	w.WriteHeader(http.StatusOK)
	w.Write(pngBytes)
}

// ---- 18-Badge AFX & NSM Badge System ----

type UserBadge struct {
	ID          string
	Name        string
	Desc        string
	HowToUnlock string
	Rarity      string
	Icon        string
	Color       string
	ColorStyle  template.CSS
	Unlocked    bool
	GrantedStr  string
}

func computeUserBadges(u *User, topicCount, postCount, likesReceived, upvoteCnt, serverKeyCount int, hasSupportPost, hasReleaseTopic bool, joinedStr string) ([]UserBadge, []UserBadge) {
	isFounder := u != nil && u.IsFounderAccount()
	isOwnerRole := isFounder || (u != nil && u.Role == "owner")
	isAdmin := isOwnerRole || (u != nil && u.Role == "admin")
	isStaff := isAdmin || (u != nil && u.Role == "staff")
	isPartner := isFounder || (u != nil && u.HasPartnerBadge())
	hasPulse := isFounder || (u != nil && u.HasPulse())

	pulseBadgeName := "AFX Pulse Subscriber"
	pulseBadgeDesc := "Active AFX Pulse Member — Unlocks Animated GIFs, Full-Page Layout Colors, Cat Ears & Moving Profile Effects."
	if u != nil {
		switch u.EffectivePulseTier() {
		case "eternal":
			pulseBadgeName = "AFX Pulse Eternal (Founder)"
			pulseBadgeDesc = "Permanent Founder Lifetime AFX Pulse — All tiers, GIFs, Full-Page Colors, Decorations & Effects unlocked forever."
		case "sovereign":
			pulseBadgeName = "AFX Pulse Sovereign"
			pulseBadgeDesc = "Top-tier AFX Pulse Sovereign Member — Full-Page Colors, GIFs, Moving Effects & 5x Server Boost Power."
		case "pro":
			pulseBadgeName = "AFX Pulse Pro"
			pulseBadgeDesc = "AFX Pulse Pro Member — Full-Page Layout Colors, GIF Avatars/Banners, Cat Ears & 2x Server Boosts."
		case "core":
			pulseBadgeName = "AFX Pulse Core"
			pulseBadgeDesc = "AFX Pulse Core Member — Custom Avatar Decorations & Animated Profile Flair."
		}
	}

	boostTierName := "Server Booster — Tier I Spark"
	effectiveBoosts := upvoteCnt
	if u != nil {
		switch u.EffectivePulseTier() {
		case "eternal":
			effectiveBoosts += 25
		case "sovereign":
			effectiveBoosts += 5
		case "pro":
			effectiveBoosts += 2
		}
	}
	switch {
	case isFounder || effectiveBoosts >= 15:
		boostTierName = "Server Booster — Tier IX Transcendent Gem"
	case effectiveBoosts >= 8:
		boostTierName = "Server Booster — Tier VI Supernova Gem"
	case effectiveBoosts >= 4:
		boostTierName = "Server Booster — Tier IV Prismatic Gem"
	case effectiveBoosts >= 2:
		boostTierName = "Server Booster — Tier II Radiant Gem"
	}

	sh := ""
	if u != nil {
		sh = strings.ToLower(strings.TrimSpace(u.SquadHouse))
	}

	mk := func(id, name, desc, how, rarity, icon, col string, unlocked bool) UserBadge {
		return UserBadge{
			ID:          id,
			Name:        name,
			Desc:        desc,
			HowToUnlock: how,
			Rarity:      rarity,
			Icon:        icon,
			Color:       col,
			ColorStyle:  template.CSS("color: " + col + "; --badge-glow: " + col + ";"),
			Unlocked:    isFounder || unlocked,
			GrantedStr:  joinedStr,
		}
	}

	all := []UserBadge{
		mk("founder_crown", "NSM Sovereign Founder",
			"Original Creator & Sovereign Founder of the NSM & AFX Platform.",
			"Reserved for the NSM Platform Founder (@Cravenz).",
			"Mythic", "crown", "#FFD700", isOwnerRole),
		mk("afx_staff", "AFX Staff",
			"Official NSM & AFX Platform Staff & Trust Safety Team.",
			"Appointed to the official AFX Staff team.",
			"Mythic", "staff_shield", "#38BDF8", isStaff),
		mk("afx_admin", "AFX Administrator",
			"Core Platform Security & Masterlist Infrastructure Administrator.",
			"Granted to AFX Platform Administrators.",
			"Mythic", "admin_star", "#F43F5E", isAdmin),
		mk("partnered_owner", "Partnered Server Owner",
			"Official AFX Partnered Community & Server Creator.",
			"Granted by AFX Staff or Administrators to standout NSM communities and creators.",
			"Legendary", "partner_infinity", "#A855F7", isPartner),
		mk("certified_mod", "Certified Moderator Alumni",
			"Certified Guardian of the AFX Forum & Multiplayer Ecosystem.",
			"Hold AFX Staff/Moderator status or earn 10+ community likes on the forum.",
			"Legendary", "mod_sword", "#10B981", isStaff || likesReceived >= 10),
		mk("afx_pulse", pulseBadgeName,
			pulseBadgeDesc,
			"Activate AFX Pulse Core, Pro, or Sovereign in the AFX Pulse tab.",
			"Legendary", "pulse_wheel", "#EC4899", hasPulse),
		mk("server_booster", boostTierName,
			"Boosted a dedicated server on the NSM Server List with evolving gem tiers (Tiers I–IX).",
			"Boost any server on /servers or subscribe to AFX Pulse Pro / Sovereign.",
			"Legendary", "boost_gem", "#F472B6", effectiveBoosts > 0),
		mk("events_vanguard", "AFX Events Vanguard",
			"Hosted or participated in official AFX Community Showcases & Events.",
			"Create a topic on the AFX Forum or feature your Favorite Server on your profile.",
			"Epic", "trophy", "#F59E0B", topicCount >= 1 || (u != nil && strings.TrimSpace(u.FavoriteServer) != "")),
		mk("house_bravery", "Vanguard of Bravery",
			"Sworn member of the AFX Vanguard House of Bravery — Fearless leaders of the front line.",
			"Select 'House of Bravery' under Vanguard Squad House in Preferences.",
			"Epic", "bravery", "#9333EA", sh == "bravery" || sh == "all"),
		mk("house_brilliance", "Vanguard of Brilliance",
			"Sworn member of the AFX Vanguard House of Brilliance — Masterminds of code and strategy.",
			"Select 'House of Brilliance' under Vanguard Squad House in Preferences.",
			"Epic", "brilliance", "#F97316", sh == "brilliance" || sh == "all"),
		mk("house_balance", "Vanguard of Balance",
			"Sworn member of the AFX Vanguard House of Balance — Architects of harmony and precision.",
			"Select 'House of Balance' under Vanguard Squad House in Preferences.",
			"Epic", "balance", "#14B8A6", sh == "balance" || sh == "all"),
		mk("bug_hunter_1", "Bug Hunter Tier I",
			"Tracked down and reported technical issues on the AFX Forum.",
			"Post in Technical Support (/forum/c/support) or contribute 2+ forum posts.",
			"Rare", "bug", "#22C55E", hasSupportPost || postCount >= 2),
		mk("bug_hunter_2", "Golden Bug Exterminator (Tier II)",
			"Elite platform debugger who goes above and beyond squashing critical bugs.",
			"Post in Technical Support and earn 5+ community likes (or hold Staff/Founder status).",
			"Legendary", "gold_bug", "#EAB308", (hasSupportPost && likesReceived >= 5) || isStaff),
		mk("active_developer", "Active Verified Developer",
			"Verified NSM Server & Resource Developer with active AFX Portal keys or GitHub repositories.",
			"Generate a Server Key in AFX Portal (/keymaster) or link your GitHub in Preferences.",
			"Epic", "code_brackets", "#3B82F6", serverKeyCount > 0 || (u != nil && strings.TrimSpace(u.GitHub) != "")),
		mk("early_verified_dev", "Early Verified Pioneer Developer",
			"Pioneering developer building scripts, MLOs, and frameworks on NSM.",
			"Publish a resource in Resource Releases (/forum/c/releases) or hold a Server Key + GitHub link.",
			"Legendary", "verified_gear", "#6366F1", hasReleaseTopic || (serverKeyCount > 0 && u != nil && strings.TrimSpace(u.GitHub) != "")),
		mk("early_supporter", "Early Platform Supporter",
			"Backed the NSM & AFX platform during the founding era.",
			"Register one of the first 50 NSM accounts or activate AFX Pulse.",
			"Legendary", "early_gem", "#F43F5E", (u != nil && u.ID > 0 && u.ID <= 50) || hasPulse),
		mk("quest_champion", "AFX Quest Champion",
			"Completed the NSM Profile & Forum Onboarding Quest.",
			"Customize your Bio & Profile Layout Colors in Preferences and post on the AFX Forum.",
			"Rare", "compass", "#06B6D4", u != nil && strings.TrimSpace(u.Bio) != "" && postCount >= 1),
		mk("legacy_pioneer", "Original Identity Pioneer",
			"Known across NSM by their verified @handle and custom Display Name.",
			"Customize your Display Name or Custom Title in Preferences.",
			"Rare", "hash_tag", "#60A5FA", u != nil && (strings.TrimSpace(u.DisplayName) != "" || strings.TrimSpace(u.Title) != "")),
	}

	var unlocked []UserBadge
	for _, b := range all {
		if b.Unlocked {
			unlocked = append(unlocked, b)
		}
	}
	return unlocked, all
}

// ---- Public Profile Page (/u/{username}) & Users Directory (/u) ----

func buildDiscourseProfileData(targetCopy *User, cur *User, tab, msg, errMsg string) map[string]interface{} {
	var upvoteCnt, serverKeyCount int
	accMu.Lock()
	if acc != nil {
		for _, ids := range acc.Upvotes {
			for _, id := range ids {
				if id == targetCopy.ID {
					upvoteCnt++
				}
			}
		}
		for _, sk := range acc.ServerKeys {
			if sk.UserID == targetCopy.ID && !sk.Revoked {
				serverKeyCount++
			}
		}
	}
	accMu.Unlock()

	joinedUnix := targetCopy.CreatedAt
	if joinedUnix > 1_000_000_000_000 {
		joinedUnix = joinedUnix / 1000
	}
	joinedStr := "Recently"
	if joinedUnix > 0 {
		joinedStr = time.Unix(joinedUnix, 0).Format("Jan 2006")
	}

	userTopics := topicsByUser(targetCopy.Username)
	userPosts := postsByUser(targetCopy.Username)

	if tab == "" {
		tab = "summary"
	}

	likesReceived := 0
	likesGiven := 0
	var lastPostSec int64
	hasSupportPost := false
	hasReleaseTopic := false
	catTopicCounts := map[string]int{}
	catReplyCounts := map[string]int{}

	forumMu.Lock()
	if forum != nil {
		for _, t := range forum.Topics {
			if strings.EqualFold(t.AuthorUsername, targetCopy.Username) {
				catTopicCounts[t.CategorySlug]++
				if t.CategorySlug == "releases" {
					hasReleaseTopic = true
				}
			}
			for _, p := range t.Posts {
				if strings.EqualFold(p.AuthorUsername, targetCopy.Username) {
					likesReceived += len(p.Likes)
					if t.CategorySlug == "support" {
						hasSupportPost = true
					}
					if p.PostNumber > 1 {
						catReplyCounts[t.CategorySlug]++
					}
					if p.CreatedAt > lastPostSec {
						lastPostSec = p.CreatedAt
					}
				}
				for _, lu := range p.Likes {
					if strings.EqualFold(lu, targetCopy.Username) {
						likesGiven++
					}
				}
			}
		}
	}
	forumMu.Unlock()

	var topCats []map[string]interface{}
	for _, c := range defaultCategories {
		tc := catTopicCounts[c.Slug]
		rc := catReplyCounts[c.Slug]
		if tc > 0 || rc > 0 {
			topCats = append(topCats, map[string]interface{}{
				"Slug":       c.Slug,
				"Name":       c.Name,
				"Color":      c.Color,
				"TopicCount": tc,
				"ReplyCount": rc,
			})
		}
	}
	if len(topCats) == 0 && len(defaultCategories) >= 2 {
		topCats = append(topCats,
			map[string]interface{}{"Slug": defaultCategories[0].Slug, "Name": defaultCategories[0].Name, "Color": defaultCategories[0].Color, "TopicCount": 1, "ReplyCount": 0},
			map[string]interface{}{"Slug": defaultCategories[1].Slug, "Name": defaultCategories[1].Name, "Color": defaultCategories[1].Color, "TopicCount": 0, "ReplyCount": 1},
		)
	}

	lastPostStr := "Recently"
	if lastPostSec > 0 {
		lastPostStr = formatRelativeTime(lastPostSec)
	}
	lastSeenSec := targetCopy.LastLogin
	if lastSeenSec > 1_000_000_000_000 {
		lastSeenSec = lastSeenSec / 1000
	}
	lastSeenStr := "just now"
	if lastSeenSec > 0 {
		lastSeenStr = formatRelativeTime(lastSeenSec)
	}

	trustLevel := "member"
	switch targetCopy.Role {
	case "owner":
		trustLevel = "founder (owner)"
	case "admin":
		trustLevel = "leader (admin)"
	case "staff":
		trustLevel = "leader (staff)"
	case "partner":
		trustLevel = "partnered creator"
	default:
		if targetCopy.IsPartner {
			trustLevel = "partnered creator"
		}
	}

	unlockedBadges, allBadges := computeUserBadges(
		targetCopy,
		len(userTopics),
		len(userPosts),
		likesReceived,
		upvoteCnt,
		serverKeyCount,
		hasSupportPost,
		hasReleaseTopic,
		joinedStr,
	)

	var enrichedTopics []map[string]interface{}
	for _, t := range userTopics {
		enrichedTopics = append(enrichedTopics, enrichTopicView(t))
	}

	var replyPosts []map[string]interface{}
	var solvedPosts []map[string]interface{}
	for _, p := range userPosts {
		if isRep, ok := p["IsReply"].(bool); ok && isRep {
			replyPosts = append(replyPosts, p)
		}
		catSlug, _ := p["CategorySlug"].(string)
		if catSlug == "support" || catSlug == "server-dev" || catSlug == "releases" {
			solvedPosts = append(solvedPosts, p)
		}
	}
	if len(replyPosts) == 0 {
		replyPosts = userPosts
	}
	if len(solvedPosts) == 0 {
		solvedPosts = userPosts
	}

	likedPosts := likedPostsForProfile(targetCopy.Username)
	readTopics := recentForumTopics(10)

	var notifications []map[string]interface{}
	for _, lp := range likedPosts {
		if len(notifications) >= 4 {
			break
		}
		notifications = append(notifications, map[string]interface{}{
			"Kind":        "liked",
			"Type":        "liked",
			"Unread":      true,
			"Icon":        "heart",
			"Color":       "#fa6c8d",
			"Title":       fmt.Sprintf("%v", lp["TopicTitle"]),
			"Excerpt":     fmt.Sprintf("%v", lp["ActionLabel"]),
			"Subtitle":    fmt.Sprintf("%v", lp["ActionLabel"]),
			"URL":         fmt.Sprintf("/forum/t/%v#post-%v", lp["TopicID"], lp["PostNumber"]),
			"RelativeStr": fmt.Sprintf("%v", lp["RelativeStr"]),
			"TimeStr":     fmt.Sprintf("%v", lp["RelativeStr"]),
		})
	}
	for _, b := range unlockedBadges {
		if len(notifications) >= 8 {
			break
		}
		notifications = append(notifications, map[string]interface{}{
			"Kind":        "badge",
			"Type":        "badge",
			"Unread":      false,
			"Icon":        "award",
			"Color":       b.Color,
			"Title":       "Unlocked '" + b.Name + "' Badge",
			"Excerpt":     b.Desc,
			"Subtitle":    b.Desc,
			"URL":         "/u/" + url.PathEscape(targetCopy.Username) + "?tab=badges",
			"RelativeStr": joinedStr,
			"TimeStr":     joinedStr,
		})
	}
	notifications = append(notifications, map[string]interface{}{
		"Kind":        "system",
		"Type":        "system",
		"Unread":      false,
		"Icon":        "shield",
		"Color":       targetCopy.EffectiveThemeAccent(),
		"Title":       "Welcome to AFX Forum & NSM Platform",
		"Excerpt":     "Your NSM account, full-page profile layout theme, and AFX Pulse perks are active.",
		"Subtitle":    "Your NSM account, full-page profile layout theme, and AFX Pulse perks are active.",
		"URL":         "/forum/t/1",
		"RelativeStr": joinedStr,
		"TimeStr":     joinedStr,
	})

	messages := []map[string]interface{}{
		{
			"ID":           1,
			"Title":        "Welcome to AFX Forum & NSM Account Sync",
			"Subject":      "Welcome to AFX Forum & NSM Account Sync",
			"Sender":       "AFX System",
			"SenderAvatar": "/img/nsm-icon.png",
			"Excerpt":      "Greetings @" + targetCopy.Username + "! Your profile is synced with NSM.exe, AFX Portal (/keymaster), and the AFX Forum. Customize your full-page Style Profile Layout Colors, Cat Ears, and Moving Profile Effects anytime in Preferences.",
			"RelativeStr":  joinedStr,
			"TimeStr":      joinedStr,
			"URL":          "/forum/t/1",
		},
		{
			"ID":           2,
			"Title":        "AFX Portal Server Key & OneSync Setup Guide",
			"Subject":      "AFX Portal Server Key & OneSync Setup Guide",
			"Sender":       "Cravenz",
			"SenderAvatar": "/avatars/u/Cravenz/64.png",
			"Excerpt":      "Need a dedicated server license key? Head over to AFX Portal (/keymaster) to generate unlimited nsmk_ server keys for your FXServer instances.",
			"RelativeStr":  lastPostStr,
			"TimeStr":      lastPostStr,
			"URL":          "/keymaster",
		},
	}

	isActivityTab := tab == "activity" || tab == "topics" || tab == "replies" || tab == "read" || tab == "likes" || tab == "solved" || tab == "votes"
	activitySub := "all"
	if isActivityTab && tab != "activity" {
		activitySub = tab
	}

	type profileThemePreset struct {
		Name        string
		Primary     string
		Accent      string
		Banner      string
		Avatar      string
		SwatchStyle template.CSS
	}
	makePreset := func(name, primary, accent, banner, avatar string) profileThemePreset {
		return profileThemePreset{
			Name:        name,
			Primary:     primary,
			Accent:      accent,
			Banner:      banner,
			Avatar:      avatar,
			SwatchStyle: template.CSS("background: linear-gradient(135deg, " + primary + " 0%, " + accent + " 100%)"),
		}
	}
	themePresets := []profileThemePreset{
		makePreset("AFX Supernova", "#2F7BFF", "#1d3557", "#1d3557", "#2F7BFF"),
		makePreset("Vinewood Horizon", "#6366F1", "#EC4899", "#1e1b4b", "#6366F1"),
		makePreset("Vespucci Sunset", "#a855f7", "#f43f5e", "#2e1065", "#a855f7"),
		makePreset("Blaine Crimson", "#f43f5e", "#9f1239", "#4c0519", "#f43f5e"),
		makePreset("Paleto Emerald", "#10b981", "#065f46", "#064e3b", "#10b981"),
		makePreset("Del Perro Abyss", "#06b6d4", "#1d4ed8", "#083344", "#06b6d4"),
		makePreset("Chiliad Sovereign", "#f59e0b", "#b45309", "#451a03", "#f59e0b"),
		makePreset("Obsidian Phantom", "#38bdf8", "#0f172a", "#0f172a", "#38bdf8"),
	}

	canManagePartner := cur != nil && (cur.Role == "staff" || cur.Role == "admin" || cur.Role == "owner" || cur.IsFounderAccount())

	return map[string]interface{}{
		"Title":            targetCopy.EffectiveDisplayName() + " (@" + targetCopy.Username + ") - AFX Forum",
		"Page":             "/forum",
		"User":             cur,
		"Profile":          targetCopy,
		"ProfileUser":      targetCopy,
		"JoinedStr":        joinedStr,
		"LastPostStr":      lastPostStr,
		"LastSeenStr":      lastSeenStr,
		"ProfileViews":     18 + len(userTopics)*9 + len(userPosts)*4,
		"TrustLevelName":   trustLevel,
		"DaysVisited":      3 + len(userTopics) + len(userPosts),
		"ReadTime":         "4h",
		"RecentReadTime":   "1h",
		"TopicsViewed":     14 + len(userTopics)*5,
		"PostsRead":        42 + len(userPosts)*8,
		"LikesGiven":       likesGiven + 3,
		"LikesReceived":    likesReceived + len(userTopics),
		"TopicCount":       len(userTopics),
		"PostCount":        len(userPosts),
		"ReplyCount":       len(replyPosts),
		"LikedCount":       len(likedPosts),
		"SolvedCount":      len(solvedPosts),
		"Topics":           userTopics,
		"EnrichedTopics":   enrichedTopics,
		"Posts":            userPosts,
		"ReplyPosts":       replyPosts,
		"LikedPosts":       likedPosts,
		"ReadTopics":       readTopics,
		"SolvedPosts":      solvedPosts,
		"Notifications":    notifications,
		"Messages":         messages,
		"TopCategories":    topCats,
		"Badges":           unlockedBadges,
		"AllBadges":        allBadges,
		"UnlockedCount":    len(unlockedBadges),
		"TotalBadgesCount": len(allBadges),
		"ThemePresets":     themePresets,
		"UpvoteCount":      upvoteCnt,
		"Tab":              tab,
		"IsActivityTab":    isActivityTab,
		"ActivitySub":      activitySub,
		"Msg":              msg,
		"Error":            errMsg,
		"IsOwn":            cur != nil && cur.ID == targetCopy.ID,
		"CanManagePartner": canManagePartner,
		"HasPending":       hasPendingClientAuth(),
		"Categories":       forumCategoriesWithCounts(),
	}
}

func userProfilePage(w http.ResponseWriter, r *http.Request) {
	rawName := r.PathValue("username")
	if rawName == "action" && r.PathValue("tab") == "send_activation_email" {
		sendJSON(w, http.StatusOK, map[string]string{"success": "OK"})
		return
	}
	if strings.HasSuffix(strings.ToLower(rawName), ".json") || wantsJSONResponse(r) {
		serveDiscourseUserJSON(w, r, strings.TrimSuffix(rawName, ".json"))
		return
	}
	accMu.Lock()
	u := findUser(rawName)
	var targetCopy *User
	if u != nil {
		cp := *u
		targetCopy = &cp
	}
	accMu.Unlock()

	if targetCopy == nil {
		w.WriteHeader(http.StatusNotFound)
		renderPage(w, r, "stub.tmpl", map[string]interface{}{"Title": "Profile Not Found", "Stub": "User @" + rawName + " not found"})
		return
	}

	cur := currentUser(r)
	tab := strings.ToLower(strings.TrimSpace(r.PathValue("tab")))
	if tab == "" {
		tab = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tab")))
	}
	if tab == "" {
		tab = "summary"
	}
	msg := ""
	if r.URL.Query().Get("saved") == "1" {
		msg = "Your profile and social links have been updated."
	}

	data := buildDiscourseProfileData(targetCopy, cur, tab, msg, "")
	renderPage(w, r, "profile.tmpl", data)
}

func usersDirectoryPage(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	period := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period")))
	if period == "" {
		period = "all"
	}

	accMu.Lock()
	var users []User
	if acc != nil {
		users = append(users, acc.Users...)
	}
	accMu.Unlock()

	var rows []map[string]interface{}
	for i := range users {
		u := users[i]
		if q != "" && !strings.Contains(strings.ToLower(u.Username), q) &&
			!strings.Contains(strings.ToLower(u.DisplayName), q) &&
			!strings.Contains(strings.ToLower(u.DiscordTag), q) &&
			!strings.Contains(strings.ToLower(u.GitHub), q) {
			continue
		}
		topics := topicsByUser(u.Username)
		posts := postsByUser(u.Username)
		cp := u
		rows = append(rows, map[string]interface{}{
			"User":          &cp,
			"TopicsCreated": len(topics),
			"RepliesPosted": len(posts),
			"LikesReceived": 4 + len(topics)*3 + len(posts)*2,
			"LikesGiven":    3 + len(posts),
			"TopicsViewed":  14 + len(topics)*5,
			"PostsRead":     42 + len(posts)*8,
			"DaysVisited":   3 + len(topics) + len(posts),
		})
	}

	renderPage(w, r, "users.tmpl", map[string]interface{}{
		"Title":      "Users - AFX Forum",
		"Page":       "/u",
		"Users":      rows,
		"TotalUsers": len(rows),
		"Query":      r.URL.Query().Get("q"),
		"Period":     period,
		"Categories": forumCategoriesWithCounts(),
	})
}

// ---- staff admin (staff, admin, owner session or bearer owner token) ----

func adminUsersPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	isStaffOrHigher := u != nil && (u.Role == "staff" || u.Role == "admin" || u.Role == "owner" || u.IsFounderAccount())
	if !isStaffOrHigher && !isOwner(r) {
		http.Error(w, "staff or owner only", http.StatusForbidden)
		return
	}
	accMu.Lock()
	users := append([]User{}, acc.Users...)
	accMu.Unlock()
	renderPage(w, r, "admin_users.tmpl", map[string]interface{}{"User": u, "Users": users, "Page": "/admin/users"})
}

func adminSetRole(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	isOwnerOrBearer := (u != nil && (u.Role == "owner" || u.IsFounderAccount())) || isOwner(r)
	isAdmin := isOwnerOrBearer || (u != nil && u.Role == "admin")
	isStaff := isAdmin || (u != nil && u.Role == "staff")
	if !isStaff {
		http.Error(w, "staff or owner only", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	role := strings.ToLower(strings.TrimSpace(r.FormValue("role")))
	if !map[string]bool{"user": true, "partner": true, "staff": true, "admin": true, "owner": true}[role] {
		http.Error(w, "bad role", http.StatusBadRequest)
		return
	}
	// Staff (non-admin/owner) can only assign "partner" or "user"
	if !isOwnerOrBearer && !isAdmin && role != "partner" && role != "user" {
		http.Error(w, "only owner/admin can assign staff/admin/owner roles", http.StatusForbidden)
		return
	}
	if !isOwnerOrBearer && role == "owner" {
		http.Error(w, "only owner can assign owner role", http.StatusForbidden)
		return
	}
	accMu.Lock()
	defer accMu.Unlock()
	t := userByID(id)
	if t == nil || ((t.Role == "owner" || t.IsFounderAccount()) && u != nil && u.ID != t.ID) {
		http.Error(w, "cannot change that user", http.StatusBadRequest)
		return
	}
	if u != nil && u.ID == t.ID && (t.Role == "owner" || t.IsFounderAccount()) && role != "owner" {
		http.Error(w, "you cannot demote yourself", http.StatusBadRequest)
		return
	}
	t.Role = role
	if role == "partner" {
		t.IsPartner = true
	}
	t.UpdatedAt = time.Now().UnixMilli()
	saveAccountsLocked()
	actor := "owner-token"
	if u != nil {
		actor = u.Username
	}
	log.Printf("[accounts] %s -> role %s (by %s)", t.Username, role, actor)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func adminTogglePartner(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	isStaff := (u != nil && (u.Role == "staff" || u.Role == "admin" || u.Role == "owner" || u.IsFounderAccount())) || isOwner(r)
	if !isStaff {
		http.Error(w, "staff, admin, or owner required to grant Partner role", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))

	accMu.Lock()
	t := userByID(id)
	if t == nil {
		accMu.Unlock()
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if t.IsFounderAccount() {
		t.IsPartner = true
	} else {
		switch action {
		case "grant":
			t.IsPartner = true
			if t.Role == "" || t.Role == "user" {
				t.Role = "partner"
			}
		case "revoke":
			t.IsPartner = false
			if t.Role == "partner" {
				t.Role = "user"
			}
		default:
			t.IsPartner = !t.IsPartner
			if t.IsPartner && (t.Role == "" || t.Role == "user") {
				t.Role = "partner"
			} else if !t.IsPartner && t.Role == "partner" {
				t.Role = "user"
			}
		}
	}
	t.UpdatedAt = time.Now().UnixMilli()
	targetCopy := *t
	saveAccountsLocked()
	accMu.Unlock()

	if wantsJSONResponse(r) {
		sendAccountJSON(w, true, "", &targetCopy)
		return
	}
	ret := strings.TrimSpace(r.FormValue("return_to"))
	if ret != "" && strings.HasPrefix(ret, "/") && !strings.HasPrefix(ret, "//") {
		http.Redirect(w, r, ret, http.StatusSeeOther)
		return
	}
	msg := "Partnered Server Owner role granted to @" + targetCopy.Username + "!"
	if !targetCopy.IsPartner {
		msg = "Partnered Server Owner role removed from @" + targetCopy.Username + "."
	}
	data := buildDiscourseProfileData(&targetCopy, u, "summary", msg, "")
	renderPage(w, r, "profile.tmpl", data)
}

// ---- upvotes (one per account per server) ----

func upvoteServer(w http.ResponseWriter, r *http.Request, ep string) {
	u := currentUser(r)
	isJSON := wantsJSONResponse(r)
	if u == nil {
		if isJSON {
			sendJSON(w, http.StatusUnauthorized, map[string]string{"error": "login"})
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/servers/"+ep), http.StatusSeeOther)
		return
	}
	accMu.Lock()
	for _, id := range acc.Upvotes[ep] {
		if id == u.ID {
			accMu.Unlock()
			if isJSON {
				sendJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": "already", "upvotes": upvoteCount(ep)})
				return
			}
			http.Redirect(w, r, "/servers/"+ep, http.StatusSeeOther)
			return
		}
	}
	acc.Upvotes[ep] = append(acc.Upvotes[ep], u.ID)
	saveAccountsLocked()
	total := len(acc.Upvotes[ep])
	accMu.Unlock()
	log.Printf("[upvote] %s upvoted %s", u.Username, ep)
	if isJSON {
		sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "upvotes": total})
		return
	}
	http.Redirect(w, r, "/servers/"+ep, http.StatusSeeOther)
}

func upvoteCount(ep string) int {
	accMu.Lock()
	defer accMu.Unlock()
	if acc == nil {
		return 0
	}
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
