package trigram

import (
	"strings"
	"testing"
)

func init() {
	Debug = true
}

func TestAdd(t *testing.T) {
	idx := NewIndex()
	data := []string{"Eric Waters", "Eric Ward", "Abc Wyx"}
	for i, str := range data {
		id := idx.Add(str)
		if id != uint64(i) {
			t.Fatalf("Adding %q (idx %d) didn't yield same doc ID (%d)", str, i, id)
		}
	}

	myNames := []string{"Eric Waters", "Eric Ward"}

	for _, test := range []struct {
		input  string
		expect []string
	}{
		{"Eric Wa", myNames},
		{"ic Wa", myNames},
		{"ric W", myNames},
		{"c Wa", myNames},
		{"c W", data},
		{" Wat", []string{"Eric Waters"}},
	} {
		var got []string
		for _, result := range idx.Query(test.input) {
			got = append(got, data[result.DocID])
		}
		if strings.Join(got, ":") != strings.Join(test.expect, ":") {
			t.Errorf("Query(%q) got %v, wanted %v", test.input, got, test.expect)
		}
	}
}

func TestSortedMaxResults(t *testing.T) {
	const max = 10
	s := NewSortedMaxResults(10)
	for i := 1; i <= max*2; i++ {
		s.MaybeAdd(uint64(i), float64(i)/100)
	}
	t.Log(s)
	if got, want := s.begin.id, uint64(max*2); got != want {
		t.Errorf("First index of list must be highest: %d != %d", got, want)
	}
	if s.count > s.max {
		t.Errorf("List size %d must not grow above %d results", s.count, s.max)
	}
	it := s.begin
	count := 0
	for {
		count++
		if it.next == nil {
			break
		}
		it = it.next
	}
	if count != s.count {
		t.Errorf("Running count differs from actual count: %d, %d", s.count, count)
	}
}

func TestTrigramData(t *testing.T) {
	//td := NewTrigramData()

}
