package workspace

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultDirs returns ["."] when input is empty, otherwise input.
func DefaultDirs(dirs []string) []string {
	if len(dirs) == 0 {
		return []string{"."}
	}
	return dirs
}

// NormalizeDirs validates, resolves symlinks, and sorts directories.
func NormalizeDirs(dirs []string) ([]string, error) {
	var res []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("invalid path: %s", d)
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("'%s' is not a directory", abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve symlinks for %s: %w", abs, err)
		}
		res = append(res, real)
	}
	sort.Strings(res)
	return res, nil
}

// DeriveSignature produces a short (<=8) hex hash of normalized dirs.
func DeriveSignature(norm []string) string {
	salt := os.Getenv("CLAUDEX_NAME_SALT")
	h := sha256.New()
	for _, p := range norm {
		v := p
		if salt != "" {
			v = salt + "|" + p
		}
		h.Write([]byte(v))
		h.Write([]byte("\n"))
	}
	sum := fmt.Sprintf("%x", h.Sum(nil))
	if len(sum) > 8 {
		return sum[:8]
	}
	return sum
}

// ToKebab lowercases and replaces non-alphanumerics with single dashes.
func ToKebab(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	var b strings.Builder
	prev := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prev = false
			continue
		}
		if !prev {
			b.WriteByte('-')
			prev = true
		}
	}
	out := b.String()
	out = strings.Trim(out, "-")
	dashSeq := regexp.MustCompile(`-+`)
	out = dashSeq.ReplaceAllString(out, "-")
	if out == "" {
		out = "ws"
	}
	return out
}

// DeriveSlug joins up to two base names of normalized dirs into a slug.
func DeriveSlug(norm []string) string {
	parts := []string{}
	for _, p := range norm {
		parts = append(parts, ToKebab(filepath.Base(p)))
		if len(parts) == 2 {
			break
		}
	}
	slug := strings.Join(parts, "-")
	if len(slug) > 24 {
		slug = strings.Trim(slug[:24], "-")
	}
	if slug == "" {
		slug = "ws"
	}
	return slug
}

// DeriveName composes the final container name from env prefix, slug and signature.
func DeriveName(slug, sig string) string {
	prefix := os.Getenv("CLAUDEX_NAME_PREFIX")
	if prefix == "" {
		prefix = "claudex"
	}
	return fmt.Sprintf("%s-%s-%s", prefix, slug, sig)
}
