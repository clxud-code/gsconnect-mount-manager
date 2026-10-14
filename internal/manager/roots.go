package manager

import "strings"

func exportPath(s string) string {
	s = "/" + strings.Trim(s, "/")
	if s == "/" {
		return s
	}
	return strings.TrimRight(s, "/")
}
func SelectRoot(exports []string, override string) string {
	if override != "" {
		for _, x := range exports {
			if exportPath(x) == exportPath(override) {
				return exportPath(x)
			}
		}
		return ""
	}
	preferred := []string{"/Primary", "/storage/emulated/0", "/"}
	for _, p := range preferred {
		for _, x := range exports {
			if exportPath(x) == p {
				return p
			}
		}
	}
	if len(exports) > 0 {
		return exportPath(exports[0])
	}
	return ""
}
