package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const backendVersion = "0.7.7-local"

var ticketPublicKey string
var uiRoutes = []string{
	"/api/servers/streamRedir/", "/api/servers/single/", "/api/servers/icon/",
	"/api/identities", "/api/ticket/identities", "/api/ticket/pubkey", "/api/upvote/",
	"/v1/public/featured-servers", "/v1/public/tweets", "/v1/public/notices/fivem",
	"/v1/public/pool-size-limits/", "/runtime/counts.json", "/api/serverlist/ingress",
}

func readTicketPublicKey(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "", fmt.Errorf("invalid public key PEM")
	}
	if _, err = x509.ParsePKCS1PublicKey(block.Bytes); err != nil {
		return "", fmt.Errorf("trusted key must be PKCS#1 RSA PUBLIC KEY: %w", err)
	}
	return base64.StdEncoding.EncodeToString(block.Bytes), nil
}

func unwrapData(data map[string]interface{}, key string) map[string]interface{} {
	if nested, ok := data[key].(map[string]interface{}); ok {
		return nested
	}
	return data
}

func validateHeartbeat(hb *heartbeat) error {
	if hb.FallbackData.Info == nil || hb.FallbackData.Dynamic == nil {
		return fmt.Errorf("fallbackData.info and fallbackData.dynamic objects are required")
	}
	if hb.Port < 0 || hb.Port > 65535 {
		return fmt.Errorf("port must be 1..65535 (0 selects default 30120)")
	}
	for key, data := range map[string]map[string]interface{}{"info": hb.FallbackData.Info, "dynamic": hb.FallbackData.Dynamic} {
		if value, exists := data[key]; exists {
			if _, ok := value.(map[string]interface{}); !ok {
				return fmt.Errorf("nested %s must be an object", key)
			}
		}
		data = unwrapData(data, key)
		for _, field := range []string{"hostname", "gametype", "mapname", "version"} {
			if value, exists := data[field]; exists && value != nil {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("%s must be a string", field)
				}
			}
		}
		if value, exists := data["vars"]; exists && value != nil {
			if _, ok := value.(map[string]interface{}); !ok {
				return fmt.Errorf("vars must be an object")
			}
		}
		if value, exists := data["resources"]; exists && value != nil {
			resources, ok := value.([]interface{})
			if !ok {
				return fmt.Errorf("resources must be an array")
			}
			for _, resource := range resources {
				if _, ok := resource.(string); !ok {
					return fmt.Errorf("resource must be a string")
				}
			}
		}
		for _, field := range []string{"clients", "svMaxclients"} {
			if value, exists := data[field]; exists {
				var text string
				switch n := value.(type) {
				case json.Number:
					text = string(n)
				case string:
					text = n
				default:
					return fmt.Errorf("%s must be an integer", field)
				}
				n, err := strconv.ParseInt(text, 10, 32)
				if err != nil || n < 0 {
					return fmt.Errorf("%s must be a non-negative int32", field)
				}
			}
		}
	}
	if len(hb.FallbackData.Players) > 0 && string(hb.FallbackData.Players) != "null" {
		var players []json.RawMessage
		if json.Unmarshal(hb.FallbackData.Players, &players) != nil {
			return fmt.Errorf("players must be an array")
		}
	}
	return nil
}

func localHandler() http.Handler {
	mux := buildMux()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCors(w)
		w.Header().Set("X-NSM-Backend", backendVersion)
		w.Header().Set("Access-Control-Expose-Headers", "X-NSM-Backend")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			sendJSON(w, 403, map[string]string{"error": "local-only API"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Explicit registrations prevent required APIs from depending on a website fallback.
func registerUIRoutes(mux *http.ServeMux) {
	h := &handler{}
	for _, path := range uiRoutes {
		mux.Handle(path, h)
	}
	mux.Handle("/api/servers/streamRedir", h)
	mux.Handle("/health", h)
}

// Authentication and ownership validation are intentionally not synthesized.
// The existing /l/ proxy passes those requests to the original authority.
func isCredentialRoute(route string) bool { return strings.Contains("lf", route) && len(route) == 1 }
