package blocks

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ewaters/meno/trigram"
	"github.com/golang/glog"
)

// An input source. Either from a file (with Size set) or from STDIN.
type ConfigSource struct {
	Input io.Reader
	Size  int
}

// The Reader config.
type Config struct {
	Source    ConfigSource
	BlockSize int

	// How many bytes should we read into the next block to build the index for
	// the given block.
	// Must be > 0 and < BlockSize.
	// This enables us to say that block "abc" contains "bcde" if the next block
	// contains "def" and IndexNextBytes is at least 2.
	IndexNextBytes int
}

// A block reader and indexer.
type Reader struct {
	Config

	reqC  chan chanRequest
	readC chan readData
	doneC chan bool
}

// An indexed block.
type Block struct {
	ID       int
	Bytes    []byte
	Newlines int
}

func (b *Block) String() string {
	return fmt.Sprintf("block { id %d, bytes %d (%q), newlines %d }", b.ID, len(b.Bytes), string(b.Bytes), b.Newlines)
}

type BlockIDOffset struct {
	BlockID int
	Offset  int
}

func (bio BlockIDOffset) String() string {
	return fmt.Sprintf("block ID %d offset %d", bio.BlockID, bio.Offset)
}

func (bio BlockIDOffset) Validate() error {
	if bio.BlockID < 0 || bio.Offset < 0 {
		return fmt.Errorf("invalid {%v}", bio)
	}
	return nil
}

func (bio BlockIDOffset) GTE(other BlockIDOffset) bool {
	if bio.BlockID == other.BlockID {
		return bio.Offset >= other.Offset
	}
	return bio.BlockID >= other.BlockID
}

func (bio BlockIDOffset) LTE(other BlockIDOffset) bool {
	if bio.BlockID == other.BlockID {
		return bio.Offset <= other.Offset
	}
	return bio.BlockID <= other.BlockID
}

type BlockIDOffsetRange struct {
	Start, End BlockIDOffset
}

func (bior BlockIDOffsetRange) Validate() error {
	if bior.Start.BlockID > bior.End.BlockID {
		return fmt.Errorf("%v: start block ID > end ID", bior)
	}
	if bior.Start.BlockID == bior.End.BlockID {
		from, to := bior.Start.Offset, bior.End.Offset
		if from > to {
			return fmt.Errorf("%v: same block offset %d > %d", bior, from, to)
		}
	}
	if err := bior.Start.Validate(); err != nil {
		return err
	}
	return bior.End.Validate()
}

func (bior BlockIDOffsetRange) String() string {
	return fmt.Sprintf("%v -> %v", bior.Start, bior.End)
}

func (bior BlockIDOffsetRange) Contains(bio BlockIDOffset) bool {
	return bior.Start.LTE(bio) && bior.End.GTE(bio)
}

func (b *Block) findNewlines() []BlockIDOffset {
	var result []BlockIDOffset
	for i, bite := range b.Bytes {
		if bite == 10 {
			result = append(result, BlockIDOffset{b.ID, i})
		}
	}
	b.Newlines = len(result)
	return result
}

// The running status of the reading from Input.
type ReadStatus struct {
	BytesRead int
	Newlines  int
	Blocks    int

	// * -1 if we don't know how many remain (if ConfigSource.Size was
	//   unset).
	// * 0 if the input is closed and read completely.
	// * >1 if we're still reading a known size.
	RemainingBytes int
}

func (rs *ReadStatus) String() string {
	return fmt.Sprintf("read %d bytes, %d new lines, %d blocks, %d remain", rs.BytesRead, rs.Newlines, rs.Blocks, rs.RemainingBytes)
}

// An Event returned from Run() passed channl.
type Event struct {
	// A new block has been read.
	NewBlock *Block

	// The current read status.
	Status ReadStatus
}

func (e Event) String() string {
	var sb strings.Builder
	if e.NewBlock != nil {
		sb.WriteString(e.NewBlock.String())
	}
	if e.Status.BytesRead != 0 {
		if sb.Len() > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(e.Status.String())
	}
	return sb.String()
}

func (e Event) Equals(other Event) bool {
	return e.String() == other.String()
}

type readData struct {
	bytesRead []byte
	readDone  bool
}

func (rd readData) String() string {
	var sb strings.Builder
	sb.WriteString("readData ")
	if r := len(rd.bytesRead); r > 0 {
		fmt.Fprintf(&sb, "bytesRead len %d", r)
	}
	if rd.readDone {
		sb.WriteString("read done")
	}
	return sb.String()
}

// A request to the internal Run() event loop.
type chanRequest struct {
	// GetBlock(id)
	// GetBlockRange(start, end)
	// GetBytes()
	getBlockRange *BlockIDOffsetRange

	// GetLine(idx)
	getLine *int

	// BlockIDsContaining(string)
	blockIDsContaining *string

	respC chan chanResponse
}

func (cr chanRequest) String() string {
	var sb strings.Builder
	sb.WriteString("chanRequest ")
	if bior := cr.getBlockRange; bior != nil {
		fmt.Fprintf(&sb, "get block range %v", *bior)
	}
	if id := cr.getLine; id != nil {
		fmt.Fprintf(&sb, "get line %d", *id)
	}
	if str := cr.blockIDsContaining; str != nil {
		fmt.Fprintf(&sb, "block IDs containing %q", *str)
	}
	return sb.String()
}

// A response from the internal Run() event loop, passed to chanRequest.respC
type chanResponse struct {
	// getBlockRange
	blocks []*Block

	// blockIDsContaining
	blockIDs []BlockIDOffset

	// getLine start and end
	blockIDOffsetRange *BlockIDOffsetRange

	err error
}

// NewReader returns a new BlockReader.
func NewReader(config Config) (*Reader, error) {
	if next := config.IndexNextBytes; next <= 0 || next > config.BlockSize {
		return nil, fmt.Errorf("Invalid IndexNextBytes %d -- must be > 0 and < BlockSize", next)
	}
	return &Reader{
		Config: config,
		reqC:   make(chan chanRequest),
		readC:  make(chan readData),
		doneC:  make(chan bool),
	}, nil
}

func (r *Reader) read() {
	buf := make([]byte, r.BlockSize+r.IndexNextBytes)
	for {
		n, err := r.Source.Input.Read(buf)
		if n > 0 {
			r.readC <- readData{
				bytesRead: append([]byte{}, buf[:n]...),
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			glog.Fatal(err)
		}
	}
	r.readC <- readData{
		readDone: true,
	}
}

func (r *Reader) Run(eventC chan Event) {
	go r.read()

	// Protected by mutex
	var mu sync.Mutex
	var blocks []*Block
	var readStatus ReadStatus
	index := trigram.NewIndex()
	var newlines []BlockIDOffset
	// End protected by mutex

	if r.Source.Size > 0 {
		readStatus.RemainingBytes = r.Source.Size
	} else {
		readStatus.RemainingBytes = -1
	}

	newBlock := func(buf, next []byte) {
		mu.Lock()
		readStatus.BytesRead += len(buf)
		if readStatus.RemainingBytes > 0 {
			readStatus.RemainingBytes -= len(buf)
		}
		id := len(blocks)
		block := &Block{
			ID:    id,
			Bytes: buf,
		}
		nls := block.findNewlines()
		newlines = append(newlines, nls...)

		//glog.Infof("Indexing %q:%q to %d", string(buf), string(next), id)
		index.AddWithID(string(buf)+string(next), uint64(id))
		readStatus.Newlines += block.Newlines
		readStatus.Blocks++
		blocks = append(blocks, block)
		event := Event{
			NewBlock: block,
			Status:   readStatus,
		}
		mu.Unlock()

		eventC <- event
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		var pendingBytes []byte
		for req := range r.readC {
			glog.V(2).Infof("Reader.Run readC %v", req)
			if req.bytesRead != nil {
				pendingBytes = append(pendingBytes, req.bytesRead...)
				block, next := r.BlockSize, r.IndexNextBytes
				if len(pendingBytes) < block+next {
					continue
				}
				newBlock(pendingBytes[:block], pendingBytes[block:block+next])
				pendingBytes = pendingBytes[block:]
				continue
			}
			if req.readDone {
				glog.Infof("Reader.Run read done")
				mu.Lock()
				readStatus.RemainingBytes = 0
				mu.Unlock()
				newBlock(pendingBytes, []byte{})
				continue
			}
		}
		wg.Done()
	}()

	for req := range r.reqC {
		glog.V(2).Infof("Reader.Run reqC %v", req)
		resp := chanResponse{}
		if bior := req.getBlockRange; bior != nil {
			start, end := bior.Start.BlockID, bior.End.BlockID

			mu.Lock()
			max := len(blocks) - 1
			mu.Unlock()

			if start > end || start < 0 || end < 0 || start > max || end > max {
				resp.err = fmt.Errorf("Invalid block range: %d -> %d (max %d)", start, end, max)
				req.respC <- resp
				continue
			}

			mu.Lock()
			resp.blocks = blocks[start : end+1]
			mu.Unlock()

			req.respC <- resp
			continue
		}
		if req.blockIDsContaining != nil {
			query := *req.blockIDsContaining

			mu.Lock()
			results := index.Query(query)
			mu.Unlock()

			for _, qr := range results {
				id := int(qr.DocID)

				offset := r.blockIDContains(id, blocks, query)
				if offset == -1 {
					continue
				}
				resp.blockIDs = append(resp.blockIDs, BlockIDOffset{
					BlockID: id,
					Offset:  offset,
				})
			}
			glog.Infof("Search %q found %d possible blocks and %d matching", query, len(results), len(resp.blockIDs))

			req.respC <- resp
			continue
		}
		if req.getLine != nil {
			mu.Lock()
			idx := *req.getLine
			if idx < 0 || idx > len(newlines)-1 {
				resp.err = fmt.Errorf("Invalid getLine idx %d; can't exceed %d", idx, len(newlines))
				mu.Unlock()
				req.respC <- resp
				continue
			}

			start, end := BlockIDOffset{0, 0}, newlines[idx]
			if idx > 0 {
				start = newlines[idx-1]
				start.Offset++
				if start.Offset > r.BlockSize {
					start.Offset = 0
					start.BlockID++
				}
			}
			resp.blockIDOffsetRange = &BlockIDOffsetRange{start, end}
			mu.Unlock()
			req.respC <- resp
			continue
		}
		glog.Fatalf("Unhandled request %v", req)
	}
	wg.Wait()
	r.doneC <- true
}

// Returns the index of the string in the block. -1 if it's not found.
func (r *Reader) blockIDContains(id int, blocks []*Block, query string) int {
	var sb strings.Builder
	sb.Write(blocks[id].Bytes)
	if id < len(blocks)-1 {
		sb.Write(blocks[id+1].Bytes[:r.IndexNextBytes])
	}
	// glog.Infof("blockIDContains(%d, %q) checking %q", id, query, sb.String())
	return strings.Index(sb.String(), query)
}

func (r *Reader) sendRequest(req chanRequest) chanResponse {
	respC := make(chan chanResponse, 1)
	req.respC = respC
	r.reqC <- req
	return <-respC
}

func (r *Reader) GetBlock(id int) (*Block, error) {
	blocks, err := r.GetBlockRange(id, id)
	if err != nil {
		return nil, err
	}
	if len(blocks) != 1 {
		return nil, fmt.Errorf("Expected one block, got %d", len(blocks))
	}
	return blocks[0], nil
}

func (r *Reader) GetBlockRange(from, to int) ([]*Block, error) {
	resp := r.sendRequest(chanRequest{
		getBlockRange: &BlockIDOffsetRange{
			BlockIDOffset{from, 0},
			BlockIDOffset{to, 0},
		},
	})
	return resp.blocks, resp.err
}

func (r *Reader) GetBytes(loc BlockIDOffsetRange) ([]byte, error) {
	if err := loc.Validate(); err != nil {
		return nil, err
	}
	blocks, err := r.GetBlockRange(loc.Start.BlockID, loc.End.BlockID)
	if err != nil {
		return nil, err
	}

	var bb bytes.Buffer
	startID, endID := loc.Start.BlockID, loc.End.BlockID
	for _, block := range blocks {
		if block.ID == startID && block.ID == endID {
			bb.Write(block.Bytes[loc.Start.Offset : loc.End.Offset+1])
		} else if block.ID == startID {
			bb.Write(block.Bytes[loc.Start.Offset:])
		} else if block.ID == endID {
			bb.Write(block.Bytes[:loc.End.Offset+1])
		} else {
			bb.Write(block.Bytes)
		}
	}
	return bb.Bytes(), nil
}

func (r *Reader) BlockIDsContaining(query string) ([]BlockIDOffset, error) {
	resp := r.sendRequest(chanRequest{
		blockIDsContaining: &query,
	})
	return resp.blockIDs, resp.err
}

// GetLine returns the range of block + offset that contain the bytes of the
// given line (which is byte range terminated by '\n').
func (r *Reader) GetLine(idx int) (*BlockIDOffsetRange, error) {
	resp := r.sendRequest(chanRequest{
		getLine: &idx,
	})
	return resp.blockIDOffsetRange, resp.err
}

func (r *Reader) Stop() {
	close(r.readC)
	close(r.reqC)
	<-r.doneC
}
