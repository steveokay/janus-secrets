package main

import (
	"sort"
	"strings"
)

// buildChildEnv overlays secrets onto the parent environment. By default a secret
// overrides a same-named parent var; with preserveEnv the parent var wins. Names
// only in one side always pass through. Output is deduplicated and sorted.
func buildChildEnv(parent []string, secrets map[string]string, preserveEnv bool) []string {
	merged := make(map[string]string, len(parent)+len(secrets))
	for _, e := range parent {
		if k, v, ok := strings.Cut(e, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range secrets {
		if preserveEnv {
			if _, exists := merged[k]; exists {
				continue
			}
		}
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
