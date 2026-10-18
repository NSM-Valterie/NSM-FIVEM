// NSM Forum: full forum.cfx.re / Discourse-style community forum persisted in forum.json.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ForumCategory struct {
	ID          int    `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	TopicCount  int    `json:"topicCount"`
	PostCount   int    `json:"postCount"`
}

type ForumPost struct {
	ID             int64    `json:"id"`
	PostNumber     int      `json:"postNumber"`
	AuthorUsername string   `json:"authorUsername"`
	Content        string   `json:"content"`
	CreatedAt      int64    `json:"createdAt"`
	Likes          []string `json:"likes,omitempty"`
}

type ForumTopic struct {
	ID             int64       `json:"id"`
	Title          string      `json:"title"`
	Slug           string      `json:"slug"`
	CategorySlug   string      `json:"categorySlug"`
	Tags           []string    `json:"tags,omitempty"`
	AuthorUsername string      `json:"authorUsername"`
	Pinned         bool        `json:"pinned,omitempty"`
	Locked         bool        `json:"locked,omitempty"`
	Views          int         `json:"views"`
	CreatedAt      int64       `json:"createdAt"`
	UpdatedAt      int64       `json:"updatedAt"`
	Posts          []ForumPost `json:"posts"`
}

func (t ForumTopic) ReplyCount() int {
	if len(t.Posts) <= 1 {
		return 0
	}
	return len(t.Posts) - 1
}

func (t ForumTopic) TotalLikes() int {
	n := 0
	for _, p := range t.Posts {
		n += len(p.Likes)
	}
	return n
}

func (t ForumTopic) Excerpt() string {
	if len(t.Posts) == 0 {
		return ""
	}
	s := strings.TrimSpace(t.Posts[0].Content)
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func (t ForumTopic) LastPoster() string {
	if len(t.Posts) == 0 {
		return t.AuthorUsername
	}
	return t.Posts[len(t.Posts)-1].AuthorUsername
}

type ForumStore struct {
	Topics     []ForumTopic `json:"topics"`
	NextTopic  int64        `json:"nextTopic"`
	NextPostID int64        `json:"nextPostId"`
}

var (
	forumMu   sync.Mutex
	forum     *ForumStore
	forumPath = "forum.json"
)

var defaultCategories = []ForumCategory{
	{ID: 1, Slug: "announcements", Name: "Announcements", Color: "#e7064f", Description: "Official NSM platform announcements, changelogs, and release notes."},
	{ID: 2, Slug: "server-dev", Name: "Server Development", Color: "#2F7BFF", Description: "Configuration, server.cfg, Keymaster keys, OneSync, and server administration."},
	{ID: 3, Slug: "releases", Name: "Resource Releases", Color: "#07baae", Description: "Share and download community resources, scripts, maps, vehicles, and UI packs."},
	{ID: 4, Slug: "bazaar", Name: "Server Bazaar", Color: "#a2ba0b", Description: "Showcase your NSM server, recruit staff, and invite players to join."},
	{ID: 5, Slug: "support", Name: "Technical Support", Color: "#f59e0b", Description: "Client launch, account linking, server connection, and troubleshooting help."},
	{ID: 6, Slug: "discussion", Name: "General Discussion", Color: "#8b5cf6", Description: "Everything else related to the NSM community and multiplayer modding."},
}

func categoryBySlug(slug string) ForumCategory {
	for _, c := range defaultCategories {
		if strings.EqualFold(c.Slug, slug) {
			return c
		}
	}
	return defaultCategories[len(defaultCategories)-1]
}

func slugifyTitle(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "topic"
	}
	if len(res) > 60 {
		res = res[:60]
	}
	return res
}

func loadForum() {
	forumMu.Lock()
	defer forumMu.Unlock()
	forum = &ForumStore{NextTopic: 1, NextPostID: 1}
	if b, err := os.ReadFile(forumPath); err == nil {
		_ = json.Unmarshal(b, forum)
	}
	if len(forum.Topics) == 0 {
		now := time.Now().Unix()
		seed := []ForumTopic{
			{
				ID:             1,
				Title:          "Welcome to the NSM Community Forum & Platform Guide",
				Slug:           "welcome-to-the-nsm-community-forum-platform-guide",
				CategorySlug:   "announcements",
				Tags:           []string{"official", "guide", "nsm"},
				AuthorUsername: "Cravenz",
				Pinned:         true,
				Views:          142,
				CreatedAt:      now - 86400*3,
				UpdatedAt:      now - 3600*6,
				Posts: []ForumPost{
					{
						ID:             1,
						PostNumber:     1,
						AuthorUsername: "Cravenz",
						CreatedAt:      now - 86400*3,
						Likes:          []string{"Cravenz"},
						Content: "Welcome to the official NSM Community Forum!\n\nEverything on this platform runs natively inside your NSM stack:\n\n1. **Unified NSM Accounts & In-App Sign-In:** Click **Link Account** inside the NSM client and it opens this website (`http://127.0.0.1/user-api-key/new`) so you can sign in with your NSM account and sync your custom profile picture and role directly into the game client.\n2. **NSM Keymaster (`/keymaster`):** Issue, manage, and revoke `nsm_srv_...` server keys for your dedicated servers.\n3. **Live Server Browser (`/servers`):** Browse live servers, inspect connected players and resources, upvote your favorites, and launch directly via `nsm://connect/`.\n4. **Custom Profiles (`/account`):** Upload a custom avatar, set your display name, custom title, banner color, and bio.",
					},
				},
			},
			{
				ID:             2,
				Title:          "How to Register a Server Key in NSM Keymaster & Configure server.cfg",
				Slug:           "how-to-register-a-server-key-in-nsm-keymaster-configure-server-cfg",
				CategorySlug:   "server-dev",
				Tags:           []string{"keymaster", "server-cfg", "tutorial"},
				AuthorUsername: "Cravenz",
				Pinned:         true,
				Views:          89,
				CreatedAt:      now - 86400*2,
				UpdatedAt:      now - 3600*4,
				Posts: []ForumPost{
					{
						ID:             2,
						PostNumber:     1,
						AuthorUsername: "Cravenz",
						CreatedAt:      now - 86400*2,
						Content: "To authenticate your dedicated server with the NSM Masterlist:\n\n1. Sign in and open **Keymaster** (`/keymaster`).\n2. Enter a **Server Name / Label** and optional **Endpoint Hint** (e.g. `127.0.0.1:30120`) and click **Generate Server Key**.\n3. Copy the one-time `sets sv_nsmServerKey \"nsm_srv_...\"` line and add it to your `server-local.cfg` or `server.cfg`.\n4. Start `nsm-server.exe`. Your server will automatically authenticate its heartbeat, strip the secret key from public listings, and bind your endpoint to your account.",
					},
				},
			},
			{
				ID:             3,
				Title:          "Signing In Inside the NSM Client & Customizing Your Profile Avatar",
				Slug:           "signing-in-inside-the-nsm-client-customizing-your-profile-avatar",
				CategorySlug:   "support",
				Tags:           []string{"account", "profile", "client"},
				AuthorUsername: "Cravenz",
				Pinned:         false,
				Views:          64,
				CreatedAt:      now - 86400,
				UpdatedAt:      now - 3600*2,
				Posts: []ForumPost{
					{
						ID:             3,
						PostNumber:     1,
						AuthorUsername: "Cravenz",
						CreatedAt:      now - 86400,
						Content: "When you click **Link Account** in the NSM client navbar, it opens our NSM Client Authorization page in your browser instead of Discord.\n\nOnce you sign in or click **Authorize & Sign In to NSM App**, your NSM client receives an RSA-encrypted session key and logs you in immediately. Visit **My Account** (`/account`) at any time to upload a custom avatar, change your display name, or set a custom title!",
					},
				},
			},
		}
		forum.Topics = seed
		forum.NextTopic = 4
		forum.NextPostID = 4
		saveForumLocked()
	}
}

func saveForumLocked() {
	b, err := json.MarshalIndent(forum, "", "  ")
	if err != nil {
		return
	}
	tmp := forumPath + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		_ = os.Rename(tmp, forumPath)
	}
}

func forumCategoriesWithCounts() []ForumCategory {
	forumMu.Lock()
	defer forumMu.Unlock()
	if forum == nil {
		return defaultCategories
	}
	cats := make([]ForumCategory, len(defaultCategories))
	copy(cats, defaultCategories)
	idx := map[string]int{}
	for i, c := range cats {
		idx[c.Slug] = i
	}
	for _, t := range forum.Topics {
		if i, ok := idx[t.CategorySlug]; ok {
			cats[i].TopicCount++
			cats[i].PostCount += len(t.Posts)
		}
	}
	return cats
}

func forumCategoriesJSON() []map[string]interface{} {
	cats := forumCategoriesWithCounts()
	out := make([]map[string]interface{}, 0, len(cats))
	for _, c := range cats {
		out = append(out, map[string]interface{}{
			"id":          c.ID,
			"name":        c.Name,
			"slug":        c.Slug,
			"color":       strings.TrimPrefix(c.Color, "#"),
			"text_color":  "FFFFFF",
			"topic_count": c.TopicCount,
			"post_count":  c.PostCount,
			"description": c.Description,
		})
	}
	return out
}

func totalForumPosts() int64 {
	forumMu.Lock()
	defer forumMu.Unlock()
	if forum == nil {
		return 0
	}
	var n int64
	for _, t := range forum.Topics {
		n += int64(len(t.Posts))
	}
	return n
}

func recentForumTopics(limit int) []map[string]interface{} {
	forumMu.Lock()
	if forum == nil {
		forumMu.Unlock()
		return nil
	}
	topics := make([]ForumTopic, len(forum.Topics))
	copy(topics, forum.Topics)
	forumMu.Unlock()

	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Pinned != topics[j].Pinned {
			return topics[i].Pinned
		}
		return topics[i].UpdatedAt > topics[j].UpdatedAt
	})
	if limit > 0 && len(topics) > limit {
		topics = topics[:limit]
	}
	out := make([]map[string]interface{}, 0, len(topics))
	for _, t := range topics {
		out = append(out, enrichTopicView(t))
	}
	return out
}

func topicsByUser(username string) []ForumTopic {
	forumMu.Lock()
	defer forumMu.Unlock()
	if forum == nil {
		return nil
	}
	var out []ForumTopic
	for _, t := range forum.Topics {
		if strings.EqualFold(t.AuthorUsername, username) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func postsByUser(username string) []map[string]interface{} {
	forumMu.Lock()
	defer forumMu.Unlock()
	if forum == nil {
		return nil
	}
	var out []map[string]interface{}
	for _, t := range forum.Topics {
		for _, p := range t.Posts {
			if strings.EqualFold(p.AuthorUsername, username) {
				excerpt := p.Content
				if len(excerpt) > 180 {
					excerpt = excerpt[:180] + "..."
				}
				out = append(out, map[string]interface{}{
					"TopicID":    t.ID,
					"TopicTitle": t.Title,
					"PostNumber": p.PostNumber,
					"Excerpt":    excerpt,
					"CreatedAt":  time.Unix(p.CreatedAt, 0).Format("Jan 02, 2006 15:04"),
					"Likes":      len(p.Likes),
				})
			}
		}
	}
	return out
}

func enrichTopicView(t ForumTopic) map[string]interface{} {
	cat := categoryBySlug(t.CategorySlug)
	accMu.Lock()
	author := findUser(t.AuthorUsername)
	var authorAvatar, authorDisplay, authorRole string
	if author != nil {
		authorAvatar = author.AvatarPath()
		authorDisplay = author.EffectiveDisplayName()
		authorRole = author.Role
	} else {
		authorAvatar = "/avatars/u/" + url.PathEscape(t.AuthorUsername) + "/64.png"
		authorDisplay = t.AuthorUsername
		authorRole = "user"
	}
	// Collect unique poster avatars (up to 4)
	seen := map[string]bool{}
	var posters []map[string]string
	for _, p := range t.Posts {
		key := strings.ToLower(p.AuthorUsername)
		if seen[key] {
			continue
		}
		seen[key] = true
		pu := findUser(p.AuthorUsername)
		av := "/avatars/u/" + url.PathEscape(p.AuthorUsername) + "/48.png"
		dn := p.AuthorUsername
		if pu != nil {
			av = pu.AvatarPath()
			dn = pu.EffectiveDisplayName()
		}
		posters = append(posters, map[string]string{
			"Username":    p.AuthorUsername,
			"DisplayName": dn,
			"Avatar":      av,
		})
		if len(posters) >= 4 {
			break
		}
	}
	accMu.Unlock()

	return map[string]interface{}{
		"ID":             t.ID,
		"Title":          t.Title,
		"Slug":           t.Slug,
		"Category":       cat,
		"Tags":           t.Tags,
		"AuthorUsername": t.AuthorUsername,
		"AuthorDisplay":  authorDisplay,
		"AuthorAvatar":   authorAvatar,
		"AuthorRole":     authorRole,
		"Pinned":         t.Pinned,
		"Locked":         t.Locked,
		"Views":          t.Views,
		"Replies":        t.ReplyCount(),
		"Likes":          t.TotalLikes(),
		"Excerpt":        t.Excerpt(),
		"Posters":        posters,
		"CreatedAtStr":   time.Unix(t.CreatedAt, 0).Format("Jan 02, 2006"),
		"UpdatedAtStr":   formatRelativeTime(t.UpdatedAt),
		"UpdatedAt":      t.UpdatedAt,
	}
}

func formatRelativeTime(unixSec int64) string {
	if unixSec <= 0 {
		return "just now"
	}
	d := time.Since(time.Unix(unixSec, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 30*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	default:
		return time.Unix(unixSec, 0).Format("Jan 02, 2006")
	}
}

// ---- Web Handlers ----

func forumPage(w http.ResponseWriter, r *http.Request) {
	catFilter := strings.TrimSpace(r.PathValue("cat"))
	if catFilter == "" {
		catFilter = strings.TrimSpace(r.URL.Query().Get("cat"))
	}
	tab := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tab")))
	if tab == "" {
		tab = "latest"
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	forumMu.Lock()
	allTopics := make([]ForumTopic, len(forum.Topics))
	copy(allTopics, forum.Topics)
	forumMu.Unlock()

	var filtered []ForumTopic
	for _, t := range allTopics {
		if catFilter != "" && !strings.EqualFold(t.CategorySlug, catFilter) {
			continue
		}
		if q != "" {
			match := strings.Contains(strings.ToLower(t.Title), q) ||
				strings.Contains(strings.ToLower(t.AuthorUsername), q)
			if !match {
				for _, tag := range t.Tags {
					if strings.Contains(strings.ToLower(tag), q) {
						match = true
						break
					}
				}
			}
			if !match && len(t.Posts) > 0 && strings.Contains(strings.ToLower(t.Posts[0].Content), q) {
				match = true
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, t)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Pinned != filtered[j].Pinned {
			return filtered[i].Pinned
		}
		if tab == "top" {
			li, lj := filtered[i].TotalLikes()*3+filtered[i].Views, filtered[j].TotalLikes()*3+filtered[j].Views
			if li != lj {
				return li > lj
			}
		}
		return filtered[i].UpdatedAt > filtered[j].UpdatedAt
	})

	views := make([]map[string]interface{}, 0, len(filtered))
	for _, t := range filtered {
		views = append(views, enrichTopicView(t))
	}

	var activeCat *ForumCategory
	if catFilter != "" {
		c := categoryBySlug(catFilter)
		activeCat = &c
	}

	renderPage(w, r, "forum.tmpl", map[string]interface{}{
		"Title":      "AFX Forum - The home of NSM",
		"Page":       "/forum",
		"Categories": forumCategoriesWithCounts(),
		"Topics":     views,
		"ActiveCat":  activeCat,
		"CatSlug":    catFilter,
		"Tab":        tab,
		"Query":      r.URL.Query().Get("q"),
	})
}

func forumCreateTopic(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next=/forum", http.StatusSeeOther)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	catSlug := strings.TrimSpace(r.FormValue("category"))
	rawTags := strings.TrimSpace(r.FormValue("tags"))
	content := strings.TrimSpace(r.FormValue("content"))

	if len(title) < 3 || len(content) < 2 {
		http.Redirect(w, r, "/forum", http.StatusSeeOther)
		return
	}
	if len(title) > 140 {
		title = title[:140]
	}
	cat := categoryBySlug(catSlug)

	var tags []string
	for _, part := range strings.Split(rawTags, ",") {
		tag := strings.ToLower(strings.TrimSpace(part))
		if tag != "" && len(tags) < 5 {
			tags = append(tags, tag)
		}
	}

	now := time.Now().Unix()
	forumMu.Lock()
	topicID := forum.NextTopic
	forum.NextTopic++
	postID := forum.NextPostID
	forum.NextPostID++

	topic := ForumTopic{
		ID:             topicID,
		Title:          title,
		Slug:           slugifyTitle(title),
		CategorySlug:   cat.Slug,
		Tags:           tags,
		AuthorUsername: u.Username,
		Views:          1,
		CreatedAt:      now,
		UpdatedAt:      now,
		Posts: []ForumPost{
			{
				ID:             postID,
				PostNumber:     1,
				AuthorUsername: u.Username,
				Content:        content,
				CreatedAt:      now,
			},
		},
	}
	forum.Topics = append(forum.Topics, topic)
	saveForumLocked()
	forumMu.Unlock()

	http.Redirect(w, r, "/forum/t/"+strconv.FormatInt(topicID, 10), http.StatusSeeOther)
}

func forumTopicPage(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimSuffix(r.PathValue("id"), ".json")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/forum", http.StatusSeeOther)
		return
	}

	forumMu.Lock()
	var found *ForumTopic
	for i := range forum.Topics {
		if forum.Topics[i].ID == id {
			forum.Topics[i].Views++
			cp := forum.Topics[i]
			cp.Posts = append([]ForumPost{}, forum.Topics[i].Posts...)
			found = &cp
			saveForumLocked()
			break
		}
	}
	forumMu.Unlock()

	if found == nil {
		w.WriteHeader(http.StatusNotFound)
		renderPage(w, r, "stub.tmpl", map[string]interface{}{"Title": "Topic Not Found", "Stub": "Forum Topic Not Found"})
		return
	}

	if strings.HasSuffix(r.URL.Path, ".json") || wantsJSONResponse(r) {
		serveDiscourseTopicJSON(w, r, *found)
		return
	}

	u := currentUser(r)
	canMod := u != nil && (roleAtLeast(u.Role, "staff") || strings.EqualFold(u.Username, found.AuthorUsername))

	cat := categoryBySlug(found.CategorySlug)
	postsView := make([]map[string]interface{}, 0, len(found.Posts))
	accMu.Lock()
	for _, p := range found.Posts {
		pu := findUser(p.AuthorUsername)
		av := "/avatars/u/" + url.PathEscape(p.AuthorUsername) + "/96.png"
		dn := p.AuthorUsername
		role := "user"
		title := "NSM Community Member"
		if pu != nil {
			av = pu.AvatarPath()
			dn = pu.EffectiveDisplayName()
			role = pu.Role
			title = pu.EffectiveTitle()
		}
		liked := false
		if u != nil {
			for _, lu := range p.Likes {
				if strings.EqualFold(lu, u.Username) {
					liked = true
					break
				}
			}
		}
		postsView = append(postsView, map[string]interface{}{
			"ID":             p.ID,
			"PostNumber":     p.PostNumber,
			"AuthorUsername": p.AuthorUsername,
			"AuthorDisplay":  dn,
			"AuthorAvatar":   av,
			"AuthorRole":     role,
			"AuthorTitle":    title,
			"Content":        p.Content,
			"Paragraphs":     strings.Split(p.Content, "\n"),
			"CreatedAtStr":   time.Unix(p.CreatedAt, 0).Format("Jan 02, 2006 15:04"),
			"RelativeStr":    formatRelativeTime(p.CreatedAt),
			"LikesCount":     len(p.Likes),
			"Liked":          liked,
		})
	}
	accMu.Unlock()

	renderPage(w, r, "forum_topic.tmpl", map[string]interface{}{
		"Title":      found.Title + " - Forum",
		"Page":       "/forum",
		"Topic":      found,
		"Category":   cat,
		"Posts":      postsView,
		"CanMod":     canMod,
		"Categories": forumCategoriesWithCounts(),
	})
}

func forumReplyTopic(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if u == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/forum/t/"+strconv.FormatInt(id, 10)), http.StatusSeeOther)
		return
	}
	content := strings.TrimSpace(r.FormValue("content"))
	if len(content) < 2 {
		http.Redirect(w, r, "/forum/t/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}

	now := time.Now().Unix()
	forumMu.Lock()
	for i := range forum.Topics {
		if forum.Topics[i].ID == id {
			if forum.Topics[i].Locked && !roleAtLeast(u.Role, "staff") {
				forumMu.Unlock()
				http.Redirect(w, r, "/forum/t/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
				return
			}
			postID := forum.NextPostID
			forum.NextPostID++
			postNum := len(forum.Topics[i].Posts) + 1
			forum.Topics[i].Posts = append(forum.Topics[i].Posts, ForumPost{
				ID:             postID,
				PostNumber:     postNum,
				AuthorUsername: u.Username,
				Content:        content,
				CreatedAt:      now,
			})
			forum.Topics[i].UpdatedAt = now
			saveForumLocked()
			break
		}
	}
	forumMu.Unlock()

	http.Redirect(w, r, "/forum/t/"+strconv.FormatInt(id, 10)+"#post-bottom", http.StatusSeeOther)
}

func forumLikePost(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	topicID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	postID, _ := strconv.ParseInt(r.PathValue("postId"), 10, 64)
	if u == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/forum/t/"+strconv.FormatInt(topicID, 10)), http.StatusSeeOther)
		return
	}

	forumMu.Lock()
	for i := range forum.Topics {
		if forum.Topics[i].ID == topicID {
			for j := range forum.Topics[i].Posts {
				if forum.Topics[i].Posts[j].ID == postID {
					likes := forum.Topics[i].Posts[j].Likes
					alreadyIdx := -1
					for k, lu := range likes {
						if strings.EqualFold(lu, u.Username) {
							alreadyIdx = k
							break
						}
					}
					if alreadyIdx >= 0 {
						forum.Topics[i].Posts[j].Likes = append(likes[:alreadyIdx], likes[alreadyIdx+1:]...)
					} else {
						forum.Topics[i].Posts[j].Likes = append(likes, u.Username)
					}
					saveForumLocked()
					break
				}
			}
			break
		}
	}
	forumMu.Unlock()

	http.Redirect(w, r, "/forum/t/"+strconv.FormatInt(topicID, 10), http.StatusSeeOther)
}

func forumModerateTopic(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	topicID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := r.PathValue("action")

	forumMu.Lock()
	for i := range forum.Topics {
		if forum.Topics[i].ID == topicID {
			isStaff := roleAtLeast(u.Role, "staff")
			isAuthor := strings.EqualFold(u.Username, forum.Topics[i].AuthorUsername)
			if !isStaff && !isAuthor {
				forumMu.Unlock()
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			switch action {
			case "pin":
				if isStaff {
					forum.Topics[i].Pinned = !forum.Topics[i].Pinned
				}
			case "lock":
				if isStaff {
					forum.Topics[i].Locked = !forum.Topics[i].Locked
				}
			case "delete":
				forum.Topics = append(forum.Topics[:i], forum.Topics[i+1:]...)
				saveForumLocked()
				forumMu.Unlock()
				http.Redirect(w, r, "/forum", http.StatusSeeOther)
				return
			}
			saveForumLocked()
			break
		}
	}
	forumMu.Unlock()

	http.Redirect(w, r, "/forum/t/"+strconv.FormatInt(topicID, 10), http.StatusSeeOther)
}

// ---- Discourse JSON endpoints (/f/posts.json, /f/t/{id}.json) ----

func serveDiscoursePostsJSON(w http.ResponseWriter, r *http.Request) {
	forumMu.Lock()
	var latest []map[string]interface{}
	if forum != nil {
		for _, t := range forum.Topics {
			for _, p := range t.Posts {
				latest = append(latest, map[string]interface{}{
					"id":              p.ID,
					"username":        p.AuthorUsername,
					"avatar_template": "/avatars/u/" + url.PathEscape(p.AuthorUsername) + "/{size}.png",
					"created_at":      time.Unix(p.CreatedAt, 0).UTC().Format(time.RFC3339),
					"cooked":          "<p>" + p.Content + "</p>",
					"post_number":     p.PostNumber,
					"post_type":       1,
					"topic_id":        t.ID,
					"topic_slug":      t.Slug,
					"topic_title":     t.Title,
				})
			}
		}
	}
	forumMu.Unlock()
	sendJSON(w, http.StatusOK, map[string]interface{}{"latest_posts": latest})
}

func serveDiscourseTopicJSON(w http.ResponseWriter, r *http.Request, t ForumTopic) {
	posts := make([]map[string]interface{}, 0, len(t.Posts))
	stream := make([]int64, 0, len(t.Posts))
	for _, p := range t.Posts {
		stream = append(stream, p.ID)
		posts = append(posts, map[string]interface{}{
			"id":              p.ID,
			"username":        p.AuthorUsername,
			"avatar_template": "/avatars/u/" + url.PathEscape(p.AuthorUsername) + "/{size}.png",
			"created_at":      time.Unix(p.CreatedAt, 0).UTC().Format(time.RFC3339),
			"cooked":          "<p>" + p.Content + "</p>",
			"post_number":     p.PostNumber,
			"post_type":       1,
			"topic_id":        t.ID,
			"topic_slug":      t.Slug,
		})
	}
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"id":          t.ID,
		"title":       t.Title,
		"fancy_title": t.Title,
		"posts_count": len(t.Posts),
		"created_at":  time.Unix(t.CreatedAt, 0).UTC().Format(time.RFC3339),
		"views":       t.Views,
		"slug":        t.Slug,
		"post_stream": map[string]interface{}{
			"posts":  posts,
			"stream": stream,
		},
	})
}

func init() {
	log.SetFlags(log.LstdFlags)
}
