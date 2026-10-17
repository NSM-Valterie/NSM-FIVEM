// NSM Local Stack - pass-through caching proxy for upstream game-file CDNs.
// Requests like /c/mirrors/... are fetched from https://content.cfx.re/mirrors/...
// once, cached under ./mirror-cache, and served locally afterwards.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var proxyRoutes = map[string]string{
	"a": "https://aka.cfx.re",
	"c": "https://content.cfx.re",
	"d": "https://docs.fivem.net",
	"e": "https://forum.fivem.net",
	"f": "https://forum.cfx.re",
	"k": "https://wiki.fivem.net",
	"l": "https://lambda.fivem.net",
	"p": "https://policy-live.fivem.net",
	"r": "https://runtime.fivem.net",
	"s": "https://static.cfx.re",
	"v": "https://private-placeholder.cfx.re",
	"w": "https://www.fivem.net",
}

func proxyRouteList() string {
	keys := make([]string, 0, len(proxyRoutes))
	for k := range proxyRoutes {
		keys = append(keys, "/"+k+"/")
	}
	return strings.Join(keys, " ")
}

func proxyRouteFor(path string) (string, string, bool) {
	if len(path) < 4 || path[0] != '/' {
		return "", "", false
	}
	route := string(path[1])
	if path[2] != '/' {
		return "", "", false
	}
	up, ok := proxyRoutes[route]
	if !ok {
		return "", "", false
	}
	return route, up, true
}

func cachePath(route, rest, query string) string {
	h := sha1.Sum([]byte(rest + "\x00" + query))
	return filepath.Join("mirror-cache", route, hex.EncodeToString(h[:]))
}

// Endpoints our stack answers itself instead of proxying:
// the client's stored forum/Discord sign-in checks get a clean "not signed in"
// (403) from US - no upstream call, no 5xx retry loops.
var localAnswers = map[string]struct {
	code int
	body string
}{
	"l/api/validate/discourse": {403, `{"error":"invalid_token"}`},
	"l/api/validate/discord":   {403, `{"error":"invalid_token"}`},
}

func serveProxy(w http.ResponseWriter, r *http.Request, route, upstream string) {
	rest := strings.TrimPrefix(r.URL.Path, "/"+route+"/")
	if la, ok := localAnswers[route+"/"+rest]; ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(la.code)
		fmt.Fprint(w, la.body)
		log.Printf("LOCAL  /%s/%s -> %d (answered by NSM backend)", route, rest, la.code)
		return
	}
	// Never log validation bodies or persist credential-bearing responses.
	cache := cachePath(route, rest, r.URL.RawQuery)
	if r.Method == http.MethodGet && !isCredentialRoute(route) {
		if f, err := os.Open(cache); err == nil {
			defer f.Close()
			st, _ := f.Stat()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
			io.Copy(w, f)
			log.Printf("CACHE  /%s/%s (%d bytes)", route, rest, st.Size())
			return
		}
	}
	url := upstream + "/" + rest
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	var body io.Reader
	if r.Body != nil && r.ContentLength != 0 {
		body = r.Body
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.ContentLength > 0 {
		req.ContentLength = r.ContentLength
	}
	for k, vv := range r.Header {
		if k == "Host" || k == "Connection" || k == "Accept-Encoding" || k == "Content-Length" {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "mirror upstream error: "+err.Error(), http.StatusBadGateway)
		log.Printf("ERR    %s: %v", url, err)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode == 200 && r.Method == http.MethodGet && !isCredentialRoute(route) {
		os.MkdirAll(filepath.Dir(cache), 0755)
		if tf, err := os.Create(cache + ".part"); err == nil {
			mw := io.MultiWriter(w, tf)
			io.Copy(mw, resp.Body)
			tf.Close()
			os.Rename(cache+".part", cache)
			log.Printf("FETCH  %s -> cached", url)
			return
		}
	}
	io.Copy(w, resp.Body)
	log.Printf("PASS   %s [%d]", url, resp.StatusCode)
}
