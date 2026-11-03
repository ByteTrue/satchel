// Key rotation and refusal backoff are selectively adapted from Magpie
// internal/gateway/search_keys.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
// Magpie is Copyright (c) 2026 yetone, MIT licensed.
package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	rateLimitRest    = time.Minute
	rateLimitRestMin = 30 * time.Second
	rateLimitRestMax = 10 * time.Minute
	turnedAwayRest   = 10 * time.Minute
	outOfCreditRest  = time.Hour
)

type keyRest struct {
	until time.Time
	why   string
}

var (
	keyRests sync.Map // keyStateID -> keyRest
	keyTurns sync.Map // provider/base -> *atomic.Uint64
)

// keyStateID hashes credentials so package state never contains a full key.
func keyStateID(providerID, key string) string {
	sum := sha256.Sum256([]byte(providerID + "\x00" + key))
	return providerID + " " + hex.EncodeToString(sum[:8])
}

func splitKeys(value string) []string {
	var keys []string
	for _, key := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	}) {
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// keyOrder rotates the starting key and puts recently refused keys last.
// Resting keys remain fallbacks, ordered by the soonest wake-up, when all
// keys are resting.
func keyOrder(providerID string, keys []string, now time.Time) []int {
	start := 0
	if len(keys) > 1 {
		value, _ := keyTurns.LoadOrStore(providerID, new(atomic.Uint64))
		start = int((value.(*atomic.Uint64).Add(1) - 1) % uint64(len(keys)))
	}
	order := make([]int, 0, len(keys))
	var resting []int
	until := make(map[int]time.Time)
	for offset := range len(keys) {
		i := (start + offset) % len(keys)
		if rest, ok := currentKeyRest(providerID, keys[i], now); ok {
			resting = append(resting, i)
			until[i] = rest.until
			continue
		}
		order = append(order, i)
	}
	slices.SortStableFunc(resting, func(a, b int) int { return until[a].Compare(until[b]) })
	return append(order, resting...)
}

func restFor(provider string, status int, retryAfter string, now time.Time) (time.Duration, string) {
	switch {
	case status == 401 || status == 403:
		return turnedAwayRest, "turned away"
	case status == 402, provider == "tavily" && (status == 432 || status == 433):
		return outOfCreditRest, "out of credit"
	}
	d := rateLimitRest
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		d = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(retryAfter); err == nil {
		d = at.Sub(now)
	}
	return min(max(d, rateLimitRestMin), rateLimitRestMax), "rate limited"
}

func restKey(providerID, key string, duration time.Duration, why string, now time.Time) {
	rest := keyRest{until: now.Add(duration), why: why}
	if current, ok := currentKeyRest(providerID, key, now); ok && current.until.After(rest.until) {
		return
	}
	keyRests.Store(keyStateID(providerID, key), rest)
}

func wakeKey(providerID, key string) {
	keyRests.Delete(keyStateID(providerID, key))
}

func currentKeyRest(providerID, key string, now time.Time) (keyRest, bool) {
	value, ok := keyRests.Load(keyStateID(providerID, key))
	if !ok {
		return keyRest{}, false
	}
	rest := value.(keyRest)
	if !now.Before(rest.until) {
		return keyRest{}, false
	}
	return rest, true
}

func resetKeyState() {
	keyRests.Clear()
	keyTurns.Clear()
}

func maskKey(key string) string {
	if strings.HasPrefix(key, "tvly-") && len(key) >= 16 {
		return "tvly-…" + key[len(key)-4:]
	}
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return key[:4] + "…" + key[len(key)-4:]
}

func keyLabel(index int, key string) string {
	if key == "" {
		key = "none"
	} else {
		key = maskKey(key)
	}
	return "key " + strconv.Itoa(index+1) + " (" + key + ")"
}

func redact(message string, keys []string) string {
	for _, key := range keys {
		if key != "" {
			message = strings.ReplaceAll(message, key, maskKey(key))
		}
	}
	return message
}
