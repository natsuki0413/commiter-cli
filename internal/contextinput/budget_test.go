package contextinput

import (
	"errors"
	"testing"
)

func TestSelectContextAutoBoundaries(t *testing.T) {
	for _, test := range []struct {
		name       string
		promptSize int
		want       int
		wantErr    error
	}{
		{"8k exact", Context8K - TemplateReserve - MinimumOutputSpace, Context8K, nil},
		{"8k plus one", Context8K - TemplateReserve - MinimumOutputSpace + 1, Context16K, nil},
		{"16k exact", Context16K - TemplateReserve - MinimumOutputSpace, Context16K, nil},
		{"16k plus one", Context16K - TemplateReserve - MinimumOutputSpace + 1, Context32K, nil},
		{"32k exact", Context32K - TemplateReserve - MinimumOutputSpace, Context32K, nil},
		{"32k plus one", Context32K - TemplateReserve - MinimumOutputSpace + 1, Context32K, ErrTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget, err := SelectContext(make([]byte, test.promptSize), 1, BudgetConfig{Context: "auto", MaxContextTokens: Context32K})
			if !errors.Is(err, test.wantErr) || budget.ContextTokens != test.want {
				t.Fatalf("budget=%#v error=%v", budget, err)
			}
		})
	}
}

func TestSelectContextHonorsAutoMaximumAndFixedContext(t *testing.T) {
	prompt := make([]byte, Context8K-TemplateReserve-MinimumOutputSpace+1)
	if budget, err := SelectContext(prompt, 1, BudgetConfig{Context: "auto", MaxContextTokens: Context8K}); !errors.Is(err, ErrTooLarge) || budget.ContextTokens != Context8K {
		t.Fatalf("auto budget=%#v error=%v", budget, err)
	}
	if budget, err := SelectContext(make([]byte, 1), 1, BudgetConfig{Context: "16k", MaxContextTokens: Context32K}); err != nil || budget.ContextTokens != Context8K {
		t.Fatalf("fixed budget=%#v error=%v", budget, err)
	}
	if budget, err := SelectContext(make([]byte, Context16K), 1, BudgetConfig{Context: "16k", MaxContextTokens: Context32K}); !errors.Is(err, ErrTooLarge) || budget.ContextTokens != Context16K {
		t.Fatalf("fixed overflow budget=%#v error=%v", budget, err)
	}
}

func TestSelectContextUsesUTF8BytesAndPerFileOutputReserve(t *testing.T) {
	prompt := []byte("日本語")
	budget, err := SelectContext(prompt, 30, BudgetConfig{Context: "auto", MaxContextTokens: Context8K})
	if err != nil {
		t.Fatal(err)
	}
	if budget.PromptBytes != len(prompt) || budget.ReservedOutputTokens != 48*30 || budget.EstimatedTokens != len(prompt)+TemplateReserve+48*30 {
		t.Fatalf("budget=%#v", budget)
	}
}

func TestSelectContextRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []BudgetConfig{{Context: "auto", MaxContextTokens: 10000}, {Context: "64k", MaxContextTokens: Context32K}} {
		if _, err := SelectContext(nil, 0, config); err == nil {
			t.Fatalf("config %#v was accepted", config)
		}
	}
}
