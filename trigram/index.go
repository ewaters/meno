package trigram

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/golang/glog"
)

var Debug bool

type Timing struct {
	Action   string
	Duration time.Duration
}

type idScore struct {
	id         interface{}
	score      float64
	prev, next *idScore
}

type SortedMaxResults struct {
	max                  int
	count                int
	begin, end           *idScore
	ignoreMaxForTopScore bool
}

func (s *SortedMaxResults) String() string {
	if s.begin == nil {
		return "Empty"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Worst: %.3f\n", s.end.score)
	fmt.Fprintf(&buf, "Full: %v\n", s.max == s.count)
	fmt.Fprintf(&buf, "List:\n")
	for i, ids := range s.list() {
		fmt.Fprintf(&buf, " %d) %d - %.3f\n", i, ids.id, ids.score)
	}
	return buf.String()
}

func (s *SortedMaxResults) list() []idScore {
	if s.begin == nil {
		return nil
	}
	ret := make([]idScore, s.count)
	it := s.begin
	i := 0
	for {
		ret[i] = idScore{id: it.id, score: it.score}
		i++
		if it.next != nil {
			it = it.next
		} else {
			break
		}
	}
	return ret
}

func NewSortedMaxResults(max int) *SortedMaxResults {
	return &SortedMaxResults{
		max: max,
	}
}

func (s *SortedMaxResults) MaybeAdd(id interface{}, score float64) {
	if s.begin == nil {
		ids := &idScore{id: id, score: score}
		s.begin = ids
		s.end = ids
		s.count++
		return
	}
	if s.end.score >= score {
		ignoreMax := s.ignoreMaxForTopScore && score == s.begin.score
		if s.count >= s.max && !ignoreMax {
			return
		}
		ids := &idScore{id: id, score: score}
		ids.prev = s.end
		s.end.next = ids
		s.end = ids
		s.count++
		return
	}
	ids := &idScore{id: id, score: score}
	if score > s.begin.score {
		ids.next = s.begin
		s.begin.prev = ids
		s.begin = ids
	} else {
		it := s.begin.next
		for {
			if score > it.score {
				ids.next = it
				ids.prev = it.prev
				it.prev.next = ids
				it.prev = ids
				break
			}
			if it.next == nil {
				panic(fmt.Sprintf("Should never get here: adding %d %.2f reached end", id, score))
			}
			it = it.next
		}
	}
	if s.count >= s.max {
		s.end.prev.next = nil
		s.end = s.end.prev
	} else {
		s.count++
	}
}

type QueryResult struct {
	DocID uint64
	Score int
}

func (qr QueryResult) String() string {
	return fmt.Sprintf("doc: %d score %d", qr.DocID, qr.Score)
}

type TrigramData struct {
	docs map[uint64]bool
}

func (d *TrigramData) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "size: %d", d.Size())
	fmt.Fprintf(&b, "docs: [")
	i := 0
	for doc := range d.docs {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d", doc)
	}
	return b.String()
}

func NewTrigramData() *TrigramData {
	return &TrigramData{
		docs: make(map[uint64]bool),
	}
}

func (d *TrigramData) Size() int { return len(d.docs) }

func (d *TrigramData) Add(id uint64) {
	d.docs[id] = true
}

func (d *TrigramData) AddFrom(rhs *TrigramData) {
	for id := range rhs.docs {
		d.docs[id] = true
	}
}

func (d *TrigramData) Docs() []uint64 {
	var result []uint64
	for id := range d.docs {
		result = append(result, id)
	}
	return result
}

func (d *TrigramData) MostFrequentDocs() []QueryResult {
	var ret []QueryResult
	for id := range d.docs {
		ret = append(ret, QueryResult{
			DocID: id,
			Score: 1,
		})
	}
	return ret
}

type Index struct {
	nextID    uint64
	maxID     uint64
	docsAdded uint64
	grams     map[Trigram]*TrigramData
}

func NewIndex() *Index {
	return &Index{
		nextID:    0,
		maxID:     0,
		docsAdded: 0,
		grams:     make(map[Trigram]*TrigramData),
	}
}

func (idx *Index) Grams() map[Trigram]*TrigramData {
	return idx.grams
}

func (idx *Index) Add(doc string) uint64 {
	docID := idx.nextID
	idx.nextID++
	idx.AddWithID(doc, docID)
	if Debug {
		fmt.Printf("Add(%q) as id %d\n", doc, docID)
	}
	return docID
}

func (idx *Index) AddWithID(doc string, docID uint64) {
	idx.docsAdded++
	if docID > idx.maxID {
		idx.maxID = docID
	}
	for _, tg := range ToTrigram(doc) {
		tgData, ok := idx.grams[tg]
		if !ok {
			tgData = NewTrigramData()
			idx.grams[tg] = tgData
		}
		tgData.Add(docID)
	}
}

func (idx *Index) Query(doc string) []QueryResult {
	tgs := ToTrigram(doc)

	docScore := make(map[uint64]int)
	for _, tg := range tgs {
		tgData, ok := idx.grams[tg]
		if !ok {
			glog.V(1).Infof("Trigram %v was not found in the index, so %q cannot be in it", tg, doc)
			return nil
		}
		for _, docID := range tgData.Docs() {
			docScore[docID]++
		}
	}

	var result []QueryResult
	for docID, score := range docScore {
		if score == len(tgs) {
			result = append(result, QueryResult{
				DocID: docID,
				Score: 1,
			})
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].DocID < result[j].DocID
	})

	glog.Infof("Query %q (%v) may be in %d indexed docs (out of %d)", doc, tgs, len(result), idx.docsAdded)
	return result
}

func (idx *Index) RemoveTrigramsWithFrequencyGreaterThan(freq float64) {
	var nuke []Trigram
	for tg, data := range idx.grams {
		l := data.Size()
		percent := float64(l) / float64(idx.docsAdded)
		if percent > freq {
			nuke = append(nuke, tg)
		}
	}
	for _, tg := range nuke {
		delete(idx.grams, tg)
	}
	if len(nuke) > 0 {
		fmt.Printf("Removed %d trigrams that had a frequency > %.2f%%\n", len(nuke), freq*100)
	}
}
