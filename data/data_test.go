package data

import (
	"testing"
)

func init() {
	enableLogger = true
}

const defaultMaxQuery = 10

func TestIndexedData(t *testing.T) {
	const width = 15
	const defaultBufSize = 10
	for _, tc := range []basicTest{
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
		tc.indexedData(t)
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
		basicTest
		matchTest []matchTest
	}{
		{
			basicTest: basicTest{
				input: "Now is the time for the good of our country",
				want: []string{
					"Now is the time",
					" for the good o",
					"f our country",
				},
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
		{
			basicTest: basicTest{
				input: "Now.\nThen.\nWhenever",
				want: []string{
					"Now.\n",
					"Then.\n",
					"Whenever",
				},
			},
			matchTest: []matchTest{
				{
					line:  0,
					query: "Now.\nThen",
					want:  true,
				},
			},
		},
	} {
		id := tc.indexedData(t)
		for _, mt := range tc.matchTest {
			got := id.lines.LineMatches(mt.line, mt.query)
			if got != mt.want {
				t.Errorf("LineMatches(%d, %q): got %v, want %v", mt.line, mt.query, got, mt.want)
			}
		}
	}
}
