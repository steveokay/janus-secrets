package main

import (
	"sort"
	"strings"
)

// buildChildEnv overlays secrets onto the parent environment. By default a secret
// overrides a same-named parent var; with preserveEnv the parent var wins. Names
// only in one side always pass through. Output is deduplicated and sorted.
//
// Secret keys that aren't valid env var identifiers (e.g. filename-style keys
// like "vigil-cloud.secrets.backup.txt") are not injectable and are reported
// back via skipped (sorted) rather than added to env.
func buildChildEnv(parent []string, secrets map[string]string, preserveEnv bool) (env []string, skipped []string) {
	merged := make(map[string]string, len(parent)+len(secrets))
	for _, e := range parent {
		if k, v, ok := strings.Cut(e, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range secrets {
		if !isEnvVarName(k) {
			skipped = append(skipped, k)
			continue
		}
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
	sort.Strings(skipped)
	return out, skipped
}
