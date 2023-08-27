package wrapper

import (
	"strings"
	"sync"
	"testing"

	"github.com/ewaters/meno/blocks"
)

func newReader(t *testing.T, input string) *blocks.Reader {
	t.Helper()
	reader, err := blocks.NewReader(blocks.Config{
		BlockSize:      5,
		IndexNextBytes: 1,
		Source: blocks.ConfigSource{
			Input: strings.NewReader(input),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestWrapper(t *testing.T) {
	reader := newReader(t, "abcdefg\n1\n2\n3")
	w, err := New(reader, 5)
	if err != nil {
		t.Fatal(err)
	}

	go w.Run()

	/*
		want := []string{
			"abcde",
			"fg\n",
			"1\n",
			"2\n",
			"3",
		}

			if got, want := w.LineCount(), len(want); got != want {
				t.Fatalf("Lines(): got %d, want %d", got, want)
			}
	*/

	w.Stop()
}

func blockRange(b1, o1, b2, o2 int) blocks.BlockIDOffsetRange {
	return blocks.BlockIDOffsetRange{
		Start: blocks.BlockIDOffset{
			BlockID: b1,
			Offset:  o1,
		},
		End: blocks.BlockIDOffset{
			BlockID: b2,
			Offset:  o2,
		},
	}
}

func TestGenerateVisibleLines(t *testing.T) {
	blockC := make(chan blocks.Block)
	lineC := make(chan visibleLine, 10)

	const width = 5

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		generateVisibleLines(width, blockC, lineC)
		wg.Done()
	}()

	blockC <- blocks.Block{
		ID:    0,
		Bytes: []byte("abcdefgh"),
	}
	got := <-lineC
	want := visibleLine{
		loc:        blockRange(0, 0, 0, 4),
		hasNewline: false,
	}

	if got.String() != want.String() {
		t.Errorf("First visible line\n got %v\nwant %v", got, want)
	}

	wg.Wait()
}
