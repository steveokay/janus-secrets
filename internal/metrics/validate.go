package metrics

import "fmt"

// validName reports whether s is a valid Prometheus metric name:
// [a-zA-Z_:][a-zA-Z0-9_:]*.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '_' || c == ':' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// validLabel reports whether s is a valid Prometheus label name:
// [a-zA-Z_][a-zA-Z0-9_]* and not reserved (no leading "__").
func validLabel(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func mustValidName(name string) {
	if !validName(name) {
		panic(fmt.Sprintf("metrics: invalid metric name %q", name))
	}
}

func mustValidLabels(labels []string) {
	seen := map[string]bool{}
	for _, l := range labels {
		if !validLabel(l) {
			panic(fmt.Sprintf("metrics: invalid label name %q", l))
		}
		if l == "le" {
			panic("metrics: label name \"le\" is reserved for histograms")
		}
		if seen[l] {
			panic(fmt.Sprintf("metrics: duplicate label name %q", l))
		}
		seen[l] = true
	}
}
