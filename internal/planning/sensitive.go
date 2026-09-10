package planning

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

type SensitiveValues struct {
	values             [][]byte
	rawCandidateValues [][]byte
}

var sensitiveKeys = map[string]bool{
	"auth": true, "credential": true, "credentials": true, "secret": true, "secrets": true,
	"token": true, "tokens": true, "password": true, "passwd": true, "api_key": true,
	"apikey": true, "access_key": true, "private_key": true, "client_secret": true, "bearer": true,
}

var (
	assignmentPattern = regexp.MustCompile(`(?im)^\s*["']?([A-Za-z_][A-Za-z0-9_-]*)["']?\s*[:=]\s*(.*?)\s*,?\s*$`)
	bearerPattern     = regexp.MustCompile(`(?i)\bBearer[ \t]+([A-Za-z0-9._~+/-]+=*)`)
	jwtPattern        = regexp.MustCompile(`\b([A-Za-z0-9_-]{2,}\.[A-Za-z0-9_-]{2,}\.[A-Za-z0-9_-]{2,})\b`)
	providerPattern   = regexp.MustCompile(`\b(?:github_pat_[A-Za-z0-9_]+|gh[pousr]_[A-Za-z0-9]+|sk-[A-Za-z0-9_-]+|xox(?:a|b|p|r|s)-[A-Za-z0-9-]+|AKIA[A-Z0-9]{16})\b`)
	uriPattern        = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://([^/@\s]+)@`)
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	nonStringPattern  = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
)

// ExtractSensitiveValues keeps extracted values in an opaque process-memory
// container. Callers can test candidates without logging or serializing values.
func ExtractSensitiveValues(contents ...[]byte) SensitiveValues {
	values := make([][]byte, 0)
	rawCandidateValues := make([][]byte, 0)
	for _, content := range contents {
		extractJSONScalars(content, &values, &rawCandidateValues)
		for _, match := range assignmentPattern.FindAllSubmatch(content, -1) {
			if sensitiveKeys[strings.ToLower(string(match[1]))] {
				value, rawCandidate := scalarValue(match[2])
				addSensitive(&values, value)
				if rawCandidate {
					addSensitive(&rawCandidateValues, value)
				}
			}
		}
		for _, pattern := range []*regexp.Regexp{bearerPattern, jwtPattern, providerPattern, privateKeyPattern} {
			for _, match := range pattern.FindAllSubmatch(content, -1) {
				value := match[0]
				if len(match) > 1 {
					value = match[1]
				}
				addCandidateSensitive(&values, &rawCandidateValues, value)
			}
		}
		for _, match := range uriPattern.FindAllSubmatch(content, -1) {
			userinfo := match[1]
			addCandidateSensitive(&values, &rawCandidateValues, userinfo)
			if separator := bytes.IndexByte(userinfo, ':'); separator >= 0 {
				addCandidateSensitive(&values, &rawCandidateValues, userinfo[separator+1:])
			}
		}
	}
	return SensitiveValues{values: values, rawCandidateValues: rawCandidateValues}
}

func (s SensitiveValues) Contains(candidate []byte) bool {
	for _, value := range s.values {
		if len(value) > 0 && bytes.Contains(candidate, value) {
			return true
		}
	}
	return false
}

func (s SensitiveValues) ContainsRawCandidate(candidate []byte) bool {
	for _, value := range s.rawCandidateValues {
		if len(value) > 0 && bytes.Contains(candidate, value) {
			return true
		}
	}
	return false
}

func extractJSONScalars(content []byte, values, rawCandidateValues *[][]byte) {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil {
		return
	}
	var walk func(any)
	walk = func(value any) {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				if sensitiveKeys[strings.ToLower(key)] {
					switch scalar := child.(type) {
					case string:
						addCandidateSensitive(values, rawCandidateValues, []byte(scalar))
					case json.Number, bool:
						addSensitive(values, []byte(strings.TrimSpace(toString(scalar))))
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range current {
				walk(child)
			}
		}
	}
	walk(decoded)
}

func toString(value any) string {
	switch scalar := value.(type) {
	case json.Number:
		return scalar.String()
	case bool:
		if scalar {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func scalarValue(value []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil, false
	}
	if trimmed[0] == '"' || trimmed[0] == '\'' {
		quote := trimmed[0]
		escaped := false
		for index := 1; index < len(trimmed); index++ {
			switch {
			case escaped:
				escaped = false
			case trimmed[index] == '\\':
				escaped = true
			case trimmed[index] == quote:
				return trimmed[1:index], true
			}
		}
		return bytes.TrimSpace(trimmed[1:]), true
	}
	for index, current := range trimmed {
		if current == '#' && (index == 0 || trimmed[index-1] == ' ' || trimmed[index-1] == '\t') {
			trimmed = bytes.TrimSpace(trimmed[:index])
			break
		}
	}
	trimmed = bytes.TrimSpace(bytes.TrimSuffix(trimmed, []byte(",")))
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	if bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) || nonStringPattern.Match(trimmed) {
		return trimmed, false
	}
	return trimmed, true
}

func addCandidateSensitive(values, rawCandidateValues *[][]byte, value []byte) {
	addSensitive(values, value)
	addSensitive(rawCandidateValues, value)
}

func addSensitive(values *[][]byte, value []byte) {
	if len(value) == 0 {
		return
	}
	for _, existing := range *values {
		if bytes.Equal(existing, value) {
			return
		}
	}
	*values = append(*values, append([]byte(nil), value...))
}
