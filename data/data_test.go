package data

import (
	"strings"
	"testing"
)

func init() {
	enableLogger = true
}

func TestIndexedData(t *testing.T) {
	const width = 15
	const defaultBufSize = 10
	for _, tc := range []struct {
		input  string
		want   []string
		resize int
	}{
		{
			input: "Now is the time for the good of our country",
			want: []string{
				"Now is the time",
				" for the good o",
				"f our country",
			},
		},
		{
			input: "Now.\nLater.\nSame thing",
			want: []string{
				"Now.\n",
				"Later.\n",
				"Same thing",
			},
		},
		{
			// This has a newline character as the last part of the buf read.
			// This is testing a specific edge case.
			input: "Right now\nNot tomorrow",
			want: []string{
				"Right now\n",
				"Not tomorrow",
			},
		},
		{
			input: "Right now\nNot tomorrow",
			want: []string{
				// 34567890
				"Right no",
				"w\n",
				"Not tomo",
				"rrow",
			},
			resize: 8,
		},
	} {
		overrideBufSize = defaultBufSize
		r := strings.NewReader(tc.input)
		id := NewIndexedData(r, width)
		if tc.resize > 0 {
			id.Resize(tc.resize)
		}
		var got []string
		for _, vl := range id.lines {
			line := vl.line
			if vl.hasNewline {
				line += "\n"
			}
			got = append(got, line)
		}

		if len(got) != len(tc.want) {
			t.Logf("\n Got: %#v\nWant: %#v", got, tc.want)
			t.Fatalf("Got %d lines, wanted %d", len(got), len(tc.want))
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Line %d: got %q, want %q", i, got[i], tc.want[i])
			}
		}
	}
}

type matchTest struct {
	line  int
	query string
	want  bool
}

func TestLineMatches(t *testing.T) {
	const width = 15
	for _, tc := range []struct {
		input     string
		want      []string
		matchTest []matchTest
	}{
		{
			input: "Now is the time for the good of our country",
			want: []string{
				"Now is the time",
				" for the good o",
				"f our country",
			},
			matchTest: []matchTest{
				{
					line:  0,
					query: "the time",
					want:  true,
				},
				{
					line:  0,
					query: "time for",
					want:  true,
				},
				{
					line:  0,
					query: "time for the good of our country",
					want:  true,
				},
				{
					line:  0,
					query: "time     for the good of our country",
					want:  false,
				},
				{
					line:  1,
					query: " for the good of ",
					want:  true,
				},
				{
					line:  2,
					query: "country",
					want:  true,
				},
			},
		},
	} {
		r := strings.NewReader(tc.input)
		id := NewIndexedData(r, width)
		var got []string
		for _, vl := range id.lines {
			line := vl.line
			if vl.hasNewline {
				line += "\n"
			}
			got = append(got, line)
		}

		if len(got) != len(tc.want) {
			t.Logf("\n Got: %#v\nWant: %#v", got, tc.want)
			t.Fatalf("Got %d lines, wanted %d", len(got), len(tc.want))
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Line %d: got %q, want %q", i, got[i], tc.want[i])
			}
		}

		for _, mt := range tc.matchTest {
			got := id.LineMatches(mt.line, mt.query)
			if got != mt.want {
				t.Errorf("LineMatches(%d, %q): got %v, want %v", mt.line, mt.query, got, mt.want)
			}
		}
	}
}