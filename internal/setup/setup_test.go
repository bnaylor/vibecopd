package setup

import (
	"bufio"
	"strings"
	"testing"
)

func TestOpenAICompatibleEndpointsCatalog(t *testing.T) {
	if len(OpenAICompatibleEndpoints) == 0 {
		t.Fatal("catalog is empty")
	}
	for i, e := range OpenAICompatibleEndpoints {
		if e.Name == "" {
			t.Errorf("entry %d: empty Name", i)
		}
		if e.URL == "" {
			t.Errorf("entry %d (%s): empty URL", i, e.Name)
		}
		if !strings.Contains(e.URL, "chat/completions") {
			t.Errorf("entry %d (%s): URL %q does not look like a chat-completions endpoint", i, e.Name, e.URL)
		}
	}
	// First entry should be OpenAI proper — empty input defaults to it.
	if OpenAICompatibleEndpoints[0].Name != "OpenAI" {
		t.Errorf("first catalog entry should be OpenAI, got %q", OpenAICompatibleEndpoints[0].Name)
	}
}

func TestPickOpenAIEndpoint_DefaultOnEmpty(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	got := pickOpenAIEndpoint(r)
	want := OpenAICompatibleEndpoints[0].URL
	if got != want {
		t.Errorf("empty input: got %q, want %q (option 1)", got, want)
	}
}

func TestPickOpenAIEndpoint_NumericSelection(t *testing.T) {
	for i, e := range OpenAICompatibleEndpoints {
		input := bufio.NewReader(strings.NewReader(itoaPlus1(i) + "\n"))
		got := pickOpenAIEndpoint(input)
		if got != e.URL {
			t.Errorf("choice %d (%s): got %q, want %q", i+1, e.Name, got, e.URL)
		}
	}
}

func TestPickOpenAIEndpoint_CustomRoutesToFreeText(t *testing.T) {
	customIdx := len(OpenAICompatibleEndpoints) + 1
	customURL := "https://my-private-llm.example.com/v1/chat/completions"
	input := bufio.NewReader(strings.NewReader(itoa(customIdx) + "\n" + customURL + "\n"))
	got := pickOpenAIEndpoint(input)
	if got != customURL {
		t.Errorf("custom path: got %q, want %q", got, customURL)
	}
}

func TestPickOpenAIEndpoint_OutOfRangeReprompts(t *testing.T) {
	// First "999" is out of range, second "2" is valid.
	input := bufio.NewReader(strings.NewReader("999\n2\n"))
	got := pickOpenAIEndpoint(input)
	want := OpenAICompatibleEndpoints[1].URL
	if got != want {
		t.Errorf("after re-prompt: got %q, want %q (option 2)", got, want)
	}
}

func TestPickOpenAIEndpoint_NonNumericReprompts(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("nope\n3\n"))
	got := pickOpenAIEndpoint(input)
	want := OpenAICompatibleEndpoints[2].URL
	if got != want {
		t.Errorf("after non-numeric re-prompt: got %q, want %q (option 3)", got, want)
	}
}

// Tiny helpers — avoids pulling fmt/strconv into test boilerplate.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func itoaPlus1(i int) string { return itoa(i + 1) }
