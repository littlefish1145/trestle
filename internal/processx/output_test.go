package processx

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
)

// Sink must serialize concurrent writes from the stdout and stderr readers, so
// a stream tee can never interleave bytes inside a writer without atomic
// writes.
func TestSyncSerializesConcurrentWriters(t *testing.T) {
	var builder strings.Builder
	tee := Sync(&builder)
	var wait sync.WaitGroup
	const writers, chunks, chunkSize = 4, 50, 37
	for index := 0; index < writers; index++ {
		payload := strings.Repeat(string(rune('a'+index)), chunkSize)
		wait.Add(1)
		go func(chunk string) {
			defer wait.Done()
			for _ = range chunks {
				if _, err := tee.Write([]byte(chunk)); err != nil {
					t.Errorf("sync write: %v", err)
				}
			}
		}(payload)
	}
	wait.Wait()
	result := builder.String()
	if len(result) != chunkSize*writers*chunks {
		t.Fatalf("serialized sink dropped bytes: %d", len(result))
	}
	for offset := 0; offset < len(result); offset += chunkSize {
		chunk := result[offset : offset+chunkSize]
		for index := 1; index < len(chunk); index++ {
			if chunk[index] != chunk[0] {
				t.Fatalf("chunk at %d mixes bytes from concurrent streams: %q", offset, chunk)
				break
			}
		}
	}
}

// The context output helpers must still roundtrip through the same values.
func TestOutputContextRoundTrip(t *testing.T) {
	ctx := WithOutput(context.Background(), io.Discard, func(string) {})
	if Output(ctx) != io.Discard {
		t.Fatal("output writer did not round trip")
	}
	notify := Notify(ctx, nil)
	if notify == nil {
		t.Fatal("a registered output with no notify still needs a usable stub")
	}
	notify("safe")
	if Output(context.Background()) != nil {
		t.Fatal("bare context must have no output writer")
	}
	seen := ""
	fallback := Notify(context.Background(), func(line string) { seen = line })
	if fallback == nil {
		t.Fatal("bare context must keep the caller fallback")
	}
	fallback("caller")
	if seen != "caller" {
		t.Fatal("fallback notify was not preserved")
	}
}
