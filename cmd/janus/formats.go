package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// sortedKeys returns m's keys in deterministic order (stable output for diffs).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// formatEnv emits KEY=value lines, single-quoting values that need it.
func formatEnv(m map[string]string) []byte {
	var b bytes.Buffer
	for _, k := range sortedKeys(m) {
		fmt.Fprintf(&b, "%s=%s\n", k, shellQuote(m[k]))
	}
	return b.Bytes()
}

// shellQuote returns v safe for a POSIX .env line: bare when it contains only
// safe characters, else single-quoted with embedded quotes escaped as '\''.
func shellQuote(v string) string {
	safe := v != "" && strings.IndexFunc(v, func(r rune) bool {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return false
		case strings.ContainsRune("_-./:@", r):
			return false
		default:
			return true
		}
	}) == -1
	if safe {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func formatJSON(m map[string]string) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func formatYAML(m map[string]string) ([]byte, error) {
	// Build an ordered mapping node so keys are sorted and values are always quoted.
	var doc yaml.Node
	doc.Kind = yaml.MappingNode
	for _, k := range sortedKeys(m) {
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Value: m[k], Style: yaml.DoubleQuotedStyle},
		)
	}
	return yaml.Marshal(&doc)
}

// formatSecrets dispatches on format name.
func formatSecrets(format string, m map[string]string) ([]byte, error) {
	switch format {
	case "env":
		return formatEnv(m), nil
	case "json":
		return formatJSON(m)
	case "yaml":
		return formatYAML(m)
	default:
		return nil, fmt.Errorf("unknown format %q (use env|json|yaml)", format)
	}
}
