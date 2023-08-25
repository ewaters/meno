package data

import (
	"strings"
	"testing"
)

func init() {
	enableLogger = true
}

type searchTest struct {
	query string
	want  int
}

type basicTest struct {
	width    int
	input    string
	resize   int
	want     []string
	maxQuery int
}

func (bt basicTest) indexedData(t *testing.T) *IndexedData {
	t.Helper()
	if bt.width == 0 {
		bt.width = 15
	}
	if bt.maxQuery == 0 {
		bt.maxQuery = 5
	}
	r := strings.NewReader(bt.input)
	logf := func(fmt string, args ...interface{}) {
		t.Logf(fmt, args...)
	}
	id := NewIndexedData(r, bt.width, bt.maxQuery, logf)
	if bt.resize > 0 {
		id.Resize(bt.resize)
	}
	var got []string
	for i := 0; i < id.lines.Size(); i++ {
		vl := id.lines.Line(i)
		line := vl.line
		if vl.hasNewline {
			line += "\n"
		}
		got = append(got, line)
	}

	if len(got) != len(bt.want) {
		t.Logf("\n Got: %#v\nWant: %#v", got, bt.want)
		t.Fatalf("Got %d lines, wanted %d", len(got), len(bt.want))
	}
	for i := range got {
		if got[i] != bt.want[i] {
			t.Fatalf("Line %d: got %q, want %q", i, got[i], bt.want[i])
		}
	}
	return id
}

func TestSearch(t *testing.T) {
	for _, tc := range []struct {
		basicTest
		tests []searchTest
	}{
		{
			basicTest: basicTest{
				input: "Now is the time\n for the good of our country",
				want: []string{
					"Now is the time\n",
					" for the good o",
					"f our country",
				},
			},
			tests: []searchTest{
				{"of", 1},
				{"country", 2},
				{"not present", -1},
				{"the time\n for", 0},
				{"the time for", -1},
			},
		},
	} {
		id := tc.indexedData(t)

		for _, st := range tc.tests {
			req := NewSearchRequest(st.query)
			go id.Search(req)

			var results []searchResult
			for result := range req.ResultC {
				results = append(results, result)
			}
			got := -1
			if len(results) == 0 {
				// Not found
			} else if len(results) == 1 {
				got = results[0].lineNumber
			} else {
				t.Fatalf("Got %d results, expected 1", len(results))
			}
			if got != st.want {
				t.Fatalf("Query for %q found on line %d, expected %d", st.query, got, st.want)
			}
		}
	}
}
