package blocks

import (
	"io"
	"testing"
)

func TestReader(t *testing.T) {
	reader, writer := io.Pipe()

	send := func(str string) {
		b := []byte(str)
		n, err := writer.Write(b)
		if err != nil {
			t.Fatalf("Write(%q) failed: %v", str, err)
		}
		if n != len(b) {
			t.Fatalf("Write(%q) wrote %d, expected %d", str, n, len(b))
		}
	}

	r, err := NewReader(Config{
		Source: ConfigSource{
			Input: reader,
		},
		BlockSize:      5,
		IndexNextBytes: 1,
	})
	if err != nil {
		t.Fatalf("NewReader(): %v", err)
	}

	eventC := make(chan Event, 1)

	go r.Run(eventC)
	defer r.Stop()

	send("abc\n123\n")

	got := <-eventC
	want := Event{
		NewBlock: &Block{
			ID:       0,
			Bytes:    []byte("abc\n1"),
			Newlines: 1,
		},
		Status: &ReadStatus{
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
		block, err := r.GetBlock(0)
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
		_, err := r.GetBlock(1)
		if err == nil {
			t.Errorf("GetBlock(1): expected an error, got no error")
		}
	}
	for _, tc := range []struct {
		query string
		want  []int
	}{
		{"bc\n1", []int{0}},
		{"c\n12", []int{0}},
		{"123\n", []int{}},
	} {
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

	writer.Close()

	got = <-eventC
	want = Event{
		NewBlock: &Block{
			ID:       1,
			Bytes:    []byte("23\n"),
			Newlines: 1,
		},
		Status: &ReadStatus{
			BytesRead:      8,
			Newlines:       2,
			Blocks:         2,
			RemainingBytes: 0,
		},
	}
	if !got.Equals(want) {
		t.Errorf("\n Got %v\nWant %v", got, want)
	}
}
