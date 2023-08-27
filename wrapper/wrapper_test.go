package wrapper

import (
	"strings"
	"sync"
	"testing"

	"github.com/ewaters/meno/blocks"
)

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
	return

	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = true

	reader := newReader(t, "abcdefg\n1\n2\n3")
	w, err := New(reader, 5, []byte("\n"))
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

func TestLineWrapper(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = false

	blockC := make(chan blocks.Block)

	lw := newLineWrapper(5, []byte("\n"))
	go lw.Run(blockC)

	blockC <- blocks.Block{
		ID:    0,
		Bytes: []byte("abcdefgh"),
	}
	blockC <- blocks.Block{
		ID:    1,
		Bytes: []byte("i\n1234567"),
	}
	close(blockC)

	lineC := make(chan visibleLine)
	var subID int

	gotLines, wantLines := 0, 4
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for line := range lineC {
			t.Logf("Got line %v", line)
			gotLines++
			if gotLines == wantLines {
				// Have the lineWrapper close lineC and clean up the
				// subscription.
				if err := lw.CancelSubscription(subID); err != nil {
					t.Fatalf("Failed to cancel subscription %d: %v", subID, err)
				}
			}
		}
		wg.Done()
	}()

	var err error
	if subID, err = lw.SubscribeLines(0, -1, lineC); err != nil {
		t.Fatalf("SubscribeLines(): %v", err)
	}

	wg.Wait()
	if gotLines != wantLines {
		t.Errorf("SubscribeLines() delivered %d, wanted %d", gotLines, wantLines)
	}

	/*
		if got, want := lw.LineCount(), 3; got != want {
			t.Errorf("LineCount(): got %d, want %d", got, want)
		}
	*/

	lw.Stop()
}

func TestGenerateVisibleLines(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = false

	blockC := make(chan blocks.Block)
	lineC := make(chan visibleLine, 10)

	assertNextLine := func(want visibleLine) {
		got := <-lineC
		if got.String() != want.String() {
			t.Errorf("\n got %v\nwant %v", got, want)
		}
	}

	const width = 5

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		generateVisibleLines([]byte("\n"), width, blockC, lineC)
		wg.Done()
	}()

	blockC <- blocks.Block{
		ID: 0,
		//             01234567
		Bytes: []byte("abcdefgh"),
	}
	assertNextLine(visibleLine{
		loc:             blockRange(0, 0, 0, 4), // "abcde"
		endsWithLineSep: false,
	})

	// Make sure that there's no other line coming yet.
	select {
	case got := <-lineC:
		t.Fatalf("There shouldn't be another line; got %v", got)
	default:
	}

	// Send another block.
	blockC <- blocks.Block{
		ID: 1,
		//             01 2345678
		Bytes: []byte("i\n1234567"),
	}

	assertNextLine(visibleLine{
		loc:             blockRange(0, 5, 1, 1), // "fghi\n"
		endsWithLineSep: true,
	})

	close(blockC)

	assertNextLine(visibleLine{
		loc:             blockRange(1, 2, 1, 6), // "12345",
		endsWithLineSep: false,
	})
	assertNextLine(visibleLine{
		loc:             blockRange(1, 7, 1, 8), // "67",
		endsWithLineSep: false,
	})

	for line := range lineC {
		t.Errorf("Got %v", line)
	}

	wg.Wait()
}
