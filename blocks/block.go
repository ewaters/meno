package blocks

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/ewaters/meno/trigram"
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
	// Must be < BlockSize.
	// This enables us to say that block "abc" contains "bcde" if the next block
	// contains "def" and IndexNextBytes is at least 2.
	IndexNextBytes int
}

// A block reader and indexer.
type Reader struct {
	Config

	reqC  chan chanRequest
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
	Status *ReadStatus
}

func (e Event) String() string {
	var sb strings.Builder
	if e.NewBlock != nil {
		sb.WriteString(e.NewBlock.String())
	}
	if e.Status != nil {
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

// A request to the internal Run() event loop.
type chanRequest struct {
	// Sent from read() goroutine
	bytesRead []byte
	readDone  bool

	// GetBlock(id)
	getBlock *int

	// BlockIDsContaining(string)
	blockIDsContaining *string

	respC chan chanResponse
}

func (cr chanRequest) String() string {
	var sb strings.Builder
	sb.WriteString("chanRequest ")
	if r := len(cr.bytesRead); r > 0 {
		fmt.Fprintf(&sb, "bytesRead len %d", r)
	}
	if cr.readDone {
		sb.WriteString("read done")
	}
	if id := cr.getBlock; id != nil {
		fmt.Fprintf(&sb, "get block %d", *id)
	}
	if str := cr.blockIDsContaining; str != nil {
		fmt.Fprintf(&sb, "block IDs containing %q", *str)
	}
	return sb.String()
}

// A response from the internal Run() event loop, passed to chanRequest.respC
type chanResponse struct {
	// getBlock
	block *Block

	// blockIDsContaining
	blockIDs []int

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
		doneC:  make(chan bool),
	}, nil
}

func (r *Reader) read() {
	buf := make([]byte, r.BlockSize+r.IndexNextBytes)
	for {
		n, err := r.Source.Input.Read(buf)
		if n > 0 {
			r.reqC <- chanRequest{
				bytesRead: append([]byte{}, buf[:n]...),
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}
	}
	r.reqC <- chanRequest{
		readDone: true,
	}
}

func (r *Reader) Run(eventC chan Event) {
	go r.read()

	var blocks []*Block
	var readStatus ReadStatus
	var pendingBytes []byte
	index := trigram.NewIndex()

	if r.Source.Size > 0 {
		readStatus.RemainingBytes = r.Source.Size
	} else {
		readStatus.RemainingBytes = -1
	}

	newBlock := func(buf, next []byte) {
		readStatus.BytesRead += len(buf)
		if readStatus.RemainingBytes > 0 {
			readStatus.RemainingBytes -= len(buf)
		}
		id := len(blocks)
		block := &Block{
			ID:       id,
			Bytes:    buf,
			Newlines: bytes.Count(buf, []byte("\n")),
		}

		composite := string(buf) + string(next)
		log.Printf("Indexing %q:%q to %d", string(buf), string(next), id)
		index.AddWithID(composite, uint64(id))
		readStatus.Newlines += block.Newlines
		readStatus.Blocks++
		blocks = append(blocks, block)
		eventC <- Event{
			NewBlock: block,
			Status:   &readStatus,
		}
	}

	for req := range r.reqC {
		log.Printf("Got req %v", req)
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
			readStatus.RemainingBytes = 0
			newBlock(pendingBytes, []byte{})
			continue
		}
		resp := chanResponse{}
		if req.getBlock != nil {
			idx := *req.getBlock
			if idx >= 0 && idx < len(blocks) {
				resp.block = blocks[idx]
			} else {
				resp.err = fmt.Errorf("Invalid block id %d", idx)
			}
			req.respC <- resp
			continue
		}
		if req.blockIDsContaining != nil {
			query := *req.blockIDsContaining
			for _, qr := range index.Query(*req.blockIDsContaining) {
				id := int(qr.DocID)
				if !r.blockIDContains(id, blocks, query) {
					continue
				}
				resp.blockIDs = append(resp.blockIDs, id)
			}
			log.Printf("Sending resp %v", resp)
			req.respC <- resp
			continue
		}
		log.Fatalf("Unhandled request %v", req)
	}
	r.doneC <- true
}

func (r *Reader) blockIDContains(id int, blocks []*Block, query string) bool {
	var sb strings.Builder
	sb.Write(blocks[id].Bytes)
	if id < len(blocks)-1 {
		sb.Write(blocks[id+1].Bytes[:r.IndexNextBytes])
	}
	// log.Printf("blockIDContains(%d, %q) checking %q", id, query, sb.String())
	return strings.Contains(sb.String(), query)
}

func (r *Reader) sendRequest(req chanRequest) chanResponse {
	respC := make(chan chanResponse, 1)
	req.respC = respC
	r.reqC <- req
	return <-respC
}

func (r *Reader) GetBlock(id int) (*Block, error) {
	resp := r.sendRequest(chanRequest{
		getBlock: &id,
	})
	return resp.block, resp.err
}

func (r *Reader) BlockIDsContaining(query string) ([]int, error) {
	resp := r.sendRequest(chanRequest{
		blockIDsContaining: &query,
	})
	return resp.blockIDs, resp.err
}

func (r *Reader) Stop() {
	close(r.reqC)
	<-r.doneC
}
