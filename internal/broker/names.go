package broker

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var invalidSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(value string) string {
	v := invalidSlug.ReplaceAllString(strings.ToLower(value), "_")
	v = strings.Trim(v, "_")
	if len(v) > 32 {
		v = v[:32]
	}
	if v == "" {
		v = "default"
	}
	return v
}

func shortHash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])[:10]
}

func templateDB(repository, name, fingerprint string) string {
	return "dbb_tpl_" + slug(name) + "_" + shortHash(repository, name, fingerprint)
}
func worktreeDB(repository, worktree string) string {
	return "dbb_wt_" + shortHash(repository, worktree)
}
