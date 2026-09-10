// Package contextinput builds and size-checks the complete local planning input.
package contextinput

import (
	"errors"
	"fmt"
)

const (
	Context8K          = 8192
	Context16K         = 16384
	Context32K         = 32768
	TemplateReserve    = 256
	MinimumOutputSpace = 1024
	OutputPerFile      = 48
)

var ErrTooLarge = errors.New("planning input exceeds the allowed context")

type BudgetConfig struct {
	Context          string
	MaxContextTokens int
}

type Budget struct {
	ContextTokens        int `json:"context_tokens"`
	EstimatedTokens      int `json:"estimated_tokens"`
	PromptBytes          int `json:"prompt_bytes"`
	TemplateTokens       int `json:"template_tokens"`
	ReservedOutputTokens int `json:"reserved_output_tokens"`
}

// SelectContext treats each UTF-8 prompt byte as one input token, then adds
// the fixed chat-template and per-file output reservations required by the SRS.
func SelectContext(prompt []byte, fileCount int, config BudgetConfig) (Budget, error) {
	if fileCount < 0 {
		return Budget{}, fmt.Errorf("file count must not be negative")
	}
	output := MinimumOutputSpace
	if perFile := OutputPerFile * fileCount; perFile > output {
		output = perFile
	}
	estimated := len(prompt) + TemplateReserve + output
	limits, err := allowedContexts(config)
	if err != nil {
		return Budget{}, err
	}
	for _, limit := range limits {
		if estimated <= limit {
			return newBudget(limit, estimated, len(prompt), output), nil
		}
	}
	return newBudget(limits[len(limits)-1], estimated, len(prompt), output), ErrTooLarge
}

func allowedContexts(config BudgetConfig) ([]int, error) {
	switch config.Context {
	case "8k":
		return []int{Context8K}, nil
	case "16k":
		return []int{Context8K, Context16K}, nil
	case "32k":
		return []int{Context8K, Context16K, Context32K}, nil
	case "auto":
		if config.MaxContextTokens != Context8K && config.MaxContextTokens != Context16K && config.MaxContextTokens != Context32K {
			return nil, fmt.Errorf("unsupported maximum context %d", config.MaxContextTokens)
		}
		limits := []int{Context8K}
		if config.MaxContextTokens >= Context16K {
			limits = append(limits, Context16K)
		}
		if config.MaxContextTokens >= Context32K {
			limits = append(limits, Context32K)
		}
		return limits, nil
	default:
		return nil, fmt.Errorf("unsupported context %q", config.Context)
	}
}

func newBudget(limit, estimated, promptBytes, output int) Budget {
	return Budget{
		ContextTokens:        limit,
		EstimatedTokens:      estimated,
		PromptBytes:          promptBytes,
		TemplateTokens:       TemplateReserve,
		ReservedOutputTokens: output,
	}
}
