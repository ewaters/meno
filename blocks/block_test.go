package blocks

import (
	"io"
	"testing"
)

type blockIDsContainsTest struct {
	query string
	want  []int
}

func (tc blockIDsContainsTest) run(t *testing.T, r *Reader) {
	t.Helper()
	ids, err := r.BlockIDsContaining(tc.query)
	if err != nil {
		t.Errorf("BlockIDsContaining(%q): %v", tc.query, err)
	} else if got, want := len(ids), len(tc.want); got != want {
		t.Logf("\n Got: %v\nWant: %v", ids, tc.want)
		t.Errorf("BlockIDsContaining(%q): got %d, expected %d", tc.query, got, want)
	} else {
		for i, got := range ids {
			want := tc.want[i]
			if got != want {
				t.Errorf("BlockIDsContaining(%q): [%d] got %d, expected %d", tc.query, i, got, want)
			}
		}
	}
}

type harness struct {
	writer *io.PipeWriter
	r      *Reader
}

var defaultConfig = Config{
	BlockSize:      5,
	IndexNextBytes: 1,
}

func newHarness(t *testing.T, config Config) *harness {
	t.Helper()

	reader, writer := io.Pipe()
	config.Source = ConfigSource{
		Input: reader,
	}

	r, err := NewReader(config)
	if err != nil {
		t.Fatalf("NewReader(): %v", err)
	}

	return &harness{
		writer: writer,
		r:      r,
	}
}

func (h *harness) send(t *testing.T, str string) {
	t.Helper()
	b := []byte(str)
	n, err := h.writer.Write(b)
	if err != nil {
		t.Fatalf("Write(%q) failed: %v", str, err)
	}
	if n != len(b) {
		t.Fatalf("Write(%q) wrote %d, expected %d", str, n, len(b))
	}
}

func (h *harness) runAndSendOnly(t *testing.T, str string) {
	t.Helper()
	eventC := make(chan Event, 1)

	go h.r.Run(eventC)

	h.send(t, str)
	h.writer.Close()

	doneC := make(chan bool)
	go func() {
		for e := range eventC {
			if e.Status.RemainingBytes == 0 {
				break
			}
		}
		doneC <- true
	}()
	<-doneC
}

func TestReader(t *testing.T) {
	h := newHarness(t, defaultConfig)

	eventC := make(chan Event, 1)

	go h.r.Run(eventC)
	defer h.r.Stop()

	h.send(t, "abc\n123\n")

	got := <-eventC
	want := Event{
		NewBlock: &Block{
			ID:       0,
			Bytes:    []byte("abc\n1"),
			Newlines: 1,
		},
		Status: ReadStatus{
			BytesRead:      5,
			Newlines:       1,
			Blocks:         1,
			RemainingBytes: -1,
		},
	}
	if !got.Equals(want) {
		t.Errorf("\n Got %v\nWant %v", got, want)
	}

	{
		block, err := h.r.GetBlock(0)
		if err != nil {
			t.Errorf("GetBlock(0): %v", err)
		} else {
			got, want := string(block.Bytes), "abc\n1"
			if got != want {
				t.Errorf("Block 0: got %q, expected %q", got, want)
			}
		}
	}
	{
		_, err := h.r.GetBlock(1)
		if err == nil {
			t.Errorf("GetBlock(1): expected an error, got no error")
		}
	}
	for _, tc := range []blockIDsContainsTest{
		{"bc\n1", []int{0}},
		{"c\n12", []int{}},
		{"23\n", []int{}},
	} {
		tc.run(t, h.r)
	}

	h.writer.Close()

	got = <-eventC
	want = Event{
		NewBlock: &Block{
			ID:       1,
			Bytes:    []byte("23\n"),
			Newlines: 1,
		},
		Status: ReadStatus{
			BytesRead:      8,
			Newlines:       2,
			Blocks:         2,
			RemainingBytes: 0,
		},
	}
	if !got.Equals(want) {
		t.Errorf("\n Got %v\nWant %v", got, want)
	}

	for _, tc := range []blockIDsContainsTest{
		{"bc\n1", []int{0}},
		// This tests the IndexNextBytes
		{"c\n12", []int{0}},
		{"23\n", []int{1}},
	} {
		tc.run(t, h.r)
	}
}

func TestNewlines(t *testing.T) {
	h := newHarness(t, defaultConfig)
	h.runAndSendOnly(t, "abc\n123\n")
	defer h.r.Stop()

	block, err := h.r.GetBlock(1)
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if got, want := string(block.Bytes), "23\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	for _, tc := range []struct {
		line    int
		want    *BlockIDOffsetRange
		wantErr bool
	}{
		{0, &BlockIDOffsetRange{BlockIDOffset{0, 0}, BlockIDOffset{0, 3}}, false},
		{1, &BlockIDOffsetRange{BlockIDOffset{0, 4}, BlockIDOffset{1, 2}}, false},
		{2, nil, true},
	} {

		got, err := h.r.GetLine(tc.line)
		if err != nil {
			if !tc.wantErr {
				t.Fatalf("GetLine(%d): got no err, wanted err", tc.line)
			}
			continue
		}
		if (got == nil) != (tc.want == nil) || got.String() != tc.want.String() {
			t.Errorf("GetLine(%d): got %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestGetBlockRange(t *testing.T) {
	h := newHarness(t, defaultConfig)
	h.runAndSendOnly(t, "abc\n123\n")
	defer h.r.Stop()

	blocks, err := h.r.GetBlockRange(0, 1)
	if err != nil {
		t.Fatalf("GetBlockRange: %v", err)
	}
	wantBlockStr := []string{"abc\n1", "23\n"}
	if got, want := len(blocks), len(wantBlockStr); got != want {
		t.Fatalf("GetBlockRange: got len %d, wanted %d", got, want)
	}

	for i, gotBlock := range blocks {
		got := string(gotBlock.Bytes)
		want := wantBlockStr[i]
		if got != want {
			t.Errorf("GetBlockRange: [%d] got %q, want %q", i, got, want)
		}
	}
}
