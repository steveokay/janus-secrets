package main

import "testing"

func TestIsEnvVarName(t *testing.T) {
	for _, k := range []string{"API_KEY", "_x", "A1"} {
		if !isEnvVarName(k) {
			t.Errorf("%q should be an env var name", k)
		}
	}
	for _, k := range []string{"", "1A", "a-b", "a.b", "vigil-cloud.secrets.backup.txt", "a/b"} {
		if isEnvVarName(k) {
			t.Errorf("%q should NOT be an env var name", k)
		}
	}
}
