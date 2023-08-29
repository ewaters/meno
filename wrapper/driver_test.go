package wrapper

import (
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ewaters/meno/blocks"
)

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
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

func assertSameStrings(t *testing.T, desc string, got, want []string) {
	t.Helper()
	if a, b := len(got), len(want); a != b {
		t.Errorf("%s: got length %d, want %d", desc, a, b)
		return
	}
	for i, a := range got {
		b := want[i]
		if a != b {
			t.Errorf("%s: [%d] got %q, want %q", desc, i, a, b)
		}
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

func TestDriver(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = true

	const width = 5
	const height = 5
	reader := newReader(t, "abcdefg\n1\n2\n3\n4\n5")
	w, err := NewDriver(reader, width, height, []byte("\n"))
	if err != nil {
		t.Fatal(err)
	}

	eventC := make(chan Event)
	go w.Run(eventC)

	got := make([]string, height)
	waitingFor := height
	for event := range eventC {
		line := event.Line
		if line == nil {
			continue
		}
		got[line.Number] = line.Line
		waitingFor--
		if waitingFor == 0 {
			break
		}
	}
	want := []string{"abcde", "fg\n", "1\n", "2\n", "3\n"}
	assertSameStrings(t, "Lines from event", got, want)

	// Make sure no more events are queued.
	time.Sleep(10 * time.Millisecond)
	select {
	case event := <-eventC:
		t.Errorf("There was another event %v, expected none", event)
	default:
	}

	// TODO: SetTopLineNumber and subscribe

	w.Stop()

	// TODO: LinesContaining which needs some way to map from block ID to
	// line numbers.

}

func TestLineWrapper(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = false

	blockC := make(chan blocks.Block)

	lw := newLineWrapper(5, []byte("\n"))
	go lw.Run(blockC, nil)

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
		for range lineC {
			//t.Logf("Got line %v", line)
			gotLines++
			if gotLines == wantLines {
				// Have the lineWrapper close lineC and clean up the
				// subscription.
				if err := lw.CancelSubscription(subID); err != nil {
					log.Fatalf("Failed to cancel subscription %d: %v", subID, err)
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

func TestGenerateVisibleLinesBlocksSameSizeAsWidth(t *testing.T) {
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
		//             01234
		Bytes: []byte("abcde"),
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
		//             012 34
		Bytes: []byte("fg\n1\n"),
	}

	assertNextLine(visibleLine{
		loc:             blockRange(1, 0, 1, 2), // "fg\n"
		endsWithLineSep: true,
	})

	close(blockC)

	assertNextLine(visibleLine{
		loc:             blockRange(1, 3, 1, 4), // "1\n",
		endsWithLineSep: true,
	})

	for line := range lineC {
		t.Errorf("Got %v", line)
	}

	wg.Wait()
}
