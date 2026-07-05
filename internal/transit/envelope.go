package transit

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// formatEnvelope renders janus:v<N>:<base64(body)>.
func formatEnvelope(version int, body []byte) string {
	return fmt.Sprintf("janus:v%d:%s", version, base64.StdEncoding.EncodeToString(body))
}

// parseEnvelope parses janus:v<N>:<base64> into (version, body). Version must be >= 1.
func parseEnvelope(s string) (int, []byte, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || parts[0] != "janus" || !strings.HasPrefix(parts[1], "v") {
		return 0, nil, ErrBadCiphertext
	}
	version, err := strconv.Atoi(parts[1][1:])
	if err != nil || version < 1 {
		return 0, nil, ErrBadCiphertext
	}
	body, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, nil, ErrBadCiphertext
	}
	return version, body, nil
}
