package wrapper

import (
	"fmt"
	"io"
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
		IndexNextBytes: 4,
		Source: blocks.ConfigSource{
			Input: strings.NewReader(input),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func waitForNLines(d *Driver, waitingFor int) []string {
	got := make([]string, 0, waitingFor)
	for event := range d.Events() {
		line := event.Line
		if line == nil {
			continue
		}
		got = append(got, line.Line)
		waitingFor--
		if waitingFor == 0 {
			break
		}
	}
	return got
}

func assertNoEventsWaiting(t *testing.T, d *Driver) {
	t.Helper()
	select {
	case event := <-d.Events():
		t.Errorf("There was another event %v, expected none", event)
	default:
	}
}

func assertResizeWindow(t *testing.T, d *Driver, width int) {
	t.Helper()
	if err := d.ResizeWindow(width); err != nil {
		t.Fatal(err)
	}
}

// Watch lines from `from` with height `height`, but stop after receiving
// `len(want)`. Assert that they equal `want`.
// Then assert there are no other events waiting.
func assertWatchedLines(t *testing.T, d *Driver, from, height int, want []string) {
	t.Helper()
	if err := d.WatchLines(from, height); err != nil {
		t.Fatal(err)
	}
	got := waitForNLines(d, len(want))
	assertSameStrings(t, fmt.Sprintf("lines from %d, height %d", from, height), got, want)
	assertNoEventsWaiting(t, d)
}

func TestDriver(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = false

	const width = 5
	const height = 5
	reader := newReader(t, "abcdefg\n1\n2\n3\n4\n5")
	d, err := NewDriver(reader, []byte("\n"))
	if err != nil {
		t.Fatal(err)
	}

	go d.Run()
	if err := d.ResizeWindow(width); err != nil {
		t.Fatal(err)
	}

	{
		if err := d.WatchLines(0, height); err != nil {
			t.Fatal(err)
		}
		got := waitForNLines(d, height)
		want := []string{"abcde", "fg\n", "1\n", "2\n", "3\n"}
		assertSameStrings(t, "Lines 0-4", got, want)
	}

	{
		if err := d.WatchLines(1, height); err != nil {
			t.Fatal(err)
		}
		got := waitForNLines(d, height)
		want := []string{"fg\n", "1\n", "2\n", "3\n", "4\n"}
		assertSameStrings(t, "Lines 1-5", got, want)
	}

	// Make sure no more events are queued.
	time.Sleep(10 * time.Millisecond)
	assertNoEventsWaiting(t, d)

	if err := d.ResizeWindow(10); err != nil {
		t.Fatal(err)
	}
	{
		if err := d.WatchLines(0, 2); err != nil {
			t.Fatal(err)
		}
		got := waitForNLines(d, 2)
		want := []string{"abcdefg\n", "1\n"}
		assertSameStrings(t, "Resized lines 0-1", got, want)
	}

	t.Logf("Done testing")
	d.Stop()

	// TODO: LinesContaining which needs some way to map from block ID to
	// line numbers.
}

func TestDriverSTDIN(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = false

	reader, writer := io.Pipe()

	blockReader, err := blocks.NewReader(blocks.Config{
		BlockSize:      5,
		IndexNextBytes: 1,
		Source: blocks.ConfigSource{
			Input: reader,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	d, err := NewDriver(blockReader, []byte("\n"))
	if err != nil {
		t.Fatal(err)
	}

	go d.Run()
	if err := d.ResizeWindow(5); err != nil {
		t.Fatal(err)
	}
	assertNoEventsWaiting(t, d)

	// This will be split into two blocks: ["abcde", "fg\n"]
	// Sine the second block is only partial, it will not result in any lines
	// displayed.
	writer.Write([]byte("abcdefg\n"))

	// Screen height is 2 but we only get one line.
	assertWatchedLines(t, d, 0, 2, []string{"abcde"})

	// Resize the window to width of 2. Since the block size is 5, the first
	// block will be split into two lines.
	if err := d.ResizeWindow(2); err != nil {
		t.Fatal(err)
	}
	assertNoEventsWaiting(t, d)

	// Screen height is 3 but we only get two lines.
	assertWatchedLines(t, d, 0, 3, []string{"ab", "cd"})

	writer.Close()

	// The writer closed, so the rest of the lines are now visible.
	assertWatchedLines(t, d, 0, 10, []string{"ab", "cd", "ef", "g\n"})

	d.Stop()
}

func TestLinesContaining(t *testing.T) {
	defer func(prev bool) { enableLogger = prev }(enableLogger)
	enableLogger = false

	// The IndexNextBytes is set to 4, so we support queries up to length 5
	reader := newReader(t, "Diane\nGeorge\nMadison\nWilliam\n")
	d, err := NewDriver(reader, []byte("\n"))
	if err != nil {
		t.Fatal(err)
	}

	go d.Run()
	defer d.Stop()

	assertResizeWindow(t, d, 80)
	assertWatchedLines(t, d, 0, 10, []string{
		"Diane\n",
		"George\n",
		"Madison\n",
		"William\n",
	})

	err = d.Search(SearchRequest{
		Query: "orge",
	})
	if err != nil {
		t.Fatal(err)
	}
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

func lor(al, ao, bl, bo int) LineOffsetRange {
	return LineOffsetRange{
		From: LineOffset{
			Line:   al,
			Offset: ao,
		},
		To: LineOffset{
			Line:   bl,
			Offset: bo,
		},
	}
}

func TestLineOffsetRangeForQueryIn(t *testing.T) {
	vlines := []*VisibleLine{
		//   012345
		{3, "abcdef"},
		{4, "ghi\n"},
		{5, "123\n"},
	}

	for _, tc := range []struct {
		query string
		want  LineOffsetRange
	}{
		{
			query: "abc",
			want:  lor(3, 0, 3, 2),
		},
		{
			query: "efgh",
			want:  lor(3, 4, 4, 1),
		},
		{
			query: "i\n123\n",
			want:  lor(4, 2, 5, 3),
		},
		{
			query: "efghi\n12",
			want:  lor(3, 4, 5, 1),
		},
	} {
		got := lineOffsetRangeForQueryIn(vlines, tc.query)
		if got == nil || got.String() != tc.want.String() {
			t.Errorf("lineOffsetRangeForQueryIn(%q)\n got %v\nwant %v", tc.query, got, tc.want)
		}
	}
}
