package gitstate

import (
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type sensitivity int

const (
	notSensitive sensitivity = iota
	sensitiveCandidate
	automaticallyExcluded
)

var candidateTokens = map[string]bool{
	"auth": true, "credential": true, "credentials": true,
	"secret": true, "secrets": true, "token": true, "tokens": true,
	"password": true, "passwd": true,
}

var privateKeyNames = map[string]bool{
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
}

func classifySensitive(repoPath string, additional []string) (sensitivity, string, error) {
	for _, pattern := range additional {
		matched, err := doublestar.PathMatch(pattern, repoPath)
		if err != nil {
			return notSensitive, "", usage("invalid additional sensitive pattern")
		}
		if matched {
			return automaticallyExcluded, "matches an additional sensitive pattern", nil
		}
	}

	lower := asciiLower(repoPath)
	components := strings.Split(lower, "/")
	base := components[len(components)-1]
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return automaticallyExcluded, "environment file", nil
	}
	if ext := path.Ext(base); ext == ".pem" || ext == ".key" {
		return automaticallyExcluded, "private key file extension", nil
	}
	if privateKeyNames[base] {
		return automaticallyExcluded, "known SSH private key name", nil
	}

	if base == ".npmrc" || base == ".pypirc" || base == ".netrc" || base == "kubeconfig" || lower == ".docker/config.json" {
		return sensitiveCandidate, "known credential-bearing configuration path", nil
	}
	for index, component := range components {
		value := component
		if index == len(components)-1 {
			value = strings.TrimSuffix(value, path.Ext(value))
		}
		for _, token := range strings.FieldsFunc(value, func(r rune) bool { return r == '.' || r == '-' || r == '_' }) {
			if candidateTokens[token] {
				return sensitiveCandidate, "path component contains sensitive token " + token, nil
			}
		}
	}
	return notSensitive, "", nil
}

func asciiLower(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
