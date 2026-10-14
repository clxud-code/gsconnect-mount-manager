package manager

import (
	"regexp"
	"strings"
	"unicode"
)

var unsafe = regexp.MustCompile(`[^\pL\pN._ -]+`)

func SafeName(name, id string) string {
	n := strings.TrimSpace(unsafe.ReplaceAllString(name, "-"))
	n = strings.Trim(n, " .-")
	if n == "" || n == "." || n == ".." {
		n = "Android"
	}
	var b strings.Builder
	for _, r := range n {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	n = b.String()
	if len([]rune(n)) > 64 {
		n = string([]rune(n)[:64])
	}
	if n == "" {
		n = "Android"
	}
	return n
}
func LinkName(name, id string, collisions map[string]string) string {
	n := SafeName(name, id)
	if old, ok := collisions[n]; ok && old != id {
		suffix := id
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		return n + "-" + suffix
	}
	return n
}
