package engine

import (
	"bytes"
	"sync"
	"testing"

	"aioverlay/internal/domain"
)

func TestNamedContextDoesNotMutateInputSnapshot(t *testing.T) {
	source := []byte("ORIGINAL_FRAME")
	original := bytes.Clone(source)
	named := []byte("NAMED")
	runner := &runner{input: Input{ContextViews: map[string]ContextView{
		"named": {Text: "context", ImageJPEG: named},
	}}}
	got := runner.contextualizeNodeInput(domain.Node{ContextMode: "named", ContextID: "named"}, nodeValue{image: source})
	if !bytes.Equal(got.image, named) || got.contextText != "context" {
		t.Fatalf("selected context image=%q text=%q", got.image, got.contextText)
	}
	if !bytes.Equal(source, original) {
		t.Fatalf("selecting a context overwrote the input snapshot: %q", source)
	}
	got.image[0] = 'X'
	if !bytes.Equal(source, original) || string(named) != "NAMED" {
		t.Fatal("attempt media shares storage with an immutable input snapshot")
	}
}

func TestNamedContextParallelAttemptsKeepIndependentImages(t *testing.T) {
	source := bytes.Repeat([]byte("o"), 4096)
	original := bytes.Clone(source)
	contexts := map[string]ContextView{
		"a": {ImageJPEG: bytes.Repeat([]byte("a"), 512)},
		"b": {ImageJPEG: bytes.Repeat([]byte("b"), 512)},
	}
	runner := &runner{input: Input{ContextViews: contexts}}
	results := make([]nodeValue, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range results {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			contextID := "a"
			if index%2 != 0 {
				contextID = "b"
			}
			results[index] = runner.contextualizeNodeInput(domain.Node{ContextMode: "named", ContextID: contextID}, nodeValue{image: source})
		}()
	}
	close(start)
	workers.Wait()
	if !bytes.Equal(source, original) {
		t.Fatal("parallel attempts overwrote the shared input snapshot")
	}
	for index, result := range results {
		contextID := "a"
		if index%2 != 0 {
			contextID = "b"
		}
		if !bytes.Equal(result.image, contexts[contextID].ImageJPEG) {
			t.Fatalf("attempt %d received another attempt's image", index)
		}
	}
	results[0].image[0] = 'X'
	for index, result := range results[1:] {
		if result.image[0] == 'X' {
			t.Fatalf("attempt %d shares mutable media with attempt 0", index+1)
		}
	}
}
