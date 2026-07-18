package main

import "regexp"

// envVarRe is the env-var identifier rule. Only keys matching it can be injected
// by `janus run`; filename-style keys (dots/dashes) are skipped.
var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isEnvVarName(k string) bool { return envVarRe.MatchString(k) }
