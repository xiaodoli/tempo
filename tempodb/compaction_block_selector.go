package tempodb

import (
	"container/heap"
	"fmt"
	"time"

	"github.com/grafana/tempo/tempodb/backend"
)

// CompactionBlockSelector is an interface for different algorithms to pick suitable blocks for compaction
type CompactionBlockSelector interface {
	BlocksToCompact() ([]*backend.BlockMeta, string)
}

/*************************** Simple Block Selector **************************/

type simpleBlockSelector struct {
	cursor             int
	blocklist          []*backend.BlockMeta
	MaxCompactionRange time.Duration
}

var _ (CompactionBlockSelector) = (*simpleBlockSelector)(nil)

func (sbs *simpleBlockSelector) BlocksToCompact() ([]*backend.BlockMeta, string) {
	// should never happen
	if inputBlocks > len(sbs.blocklist) {
		return nil, ""
	}

	for sbs.cursor < len(sbs.blocklist)-inputBlocks+1 {
		cursorEnd := sbs.cursor + inputBlocks - 1
		if sbs.blocklist[cursorEnd].EndTime.Sub(sbs.blocklist[sbs.cursor].StartTime) < sbs.MaxCompactionRange {
			startPos := sbs.cursor
			sbs.cursor = startPos + inputBlocks
			hashString := sbs.blocklist[startPos].TenantID

			return sbs.blocklist[startPos : startPos+inputBlocks], hashString
		}
		sbs.cursor++
	}

	return nil, ""
}

/*************************** Time Window Block Selector **************************/

// Sharding will be based on time slot - not level. Since each compactor works on two levels.
// Levels will be needed for id-range isolation
// The timeWindowBlockSelector can be used ONLY ONCE PER TIMESLOT.
// It needs to be reinitialized with updated blocklist.

type timeWindowBlockSelector struct {
	cursor             int
	blocklist          []*backend.BlockMeta
	MaxCompactionRange time.Duration // Size of the time window - say 6 hours
}

var _ (CompactionBlockSelector) = (*timeWindowBlockSelector)(nil)

func newTimeWindowBlockSelector(blocklist []*backend.BlockMeta, maxCompactionRange time.Duration) CompactionBlockSelector {
	twbs := &timeWindowBlockSelector{
		blocklist:          blocklist,
		MaxCompactionRange: maxCompactionRange,
	}

	return twbs
}

func (twbs *timeWindowBlockSelector) BlocksToCompact() ([]*backend.BlockMeta, string) {
	var blocksToCompact BlockMetaHeap

	for twbs.cursor < len(twbs.blocklist) {
		blocksToCompact = BlockMetaHeap(make([]*backend.BlockMeta, 0))
		heap.Init(&blocksToCompact)

		// find everything from cursor forward that belongs to this block
		cursorEnd := twbs.cursor + 1
		cursorBlock := twbs.blocklist[twbs.cursor]
		currentWindow := twbs.windowForBlock(cursorBlock)

		heap.Push(&blocksToCompact, cursorBlock)

		for cursorEnd < len(twbs.blocklist) {
			if currentWindow != twbs.windowForBlock(twbs.blocklist[cursorEnd]) {
				break
			}

			heap.Push(&blocksToCompact, twbs.blocklist[cursorEnd])
			cursorEnd++
		}

		// if we found enough blocks, huzzah!  return them and we'll check this time window again next loop
		if len(blocksToCompact) >= inputBlocks {
			// pop all
			for len(blocksToCompact) > inputBlocks {
				heap.Pop(&blocksToCompact)
			}

			return blocksToCompact, fmt.Sprintf("%v-%v", cursorBlock.TenantID, currentWindow)
		}

		// otherwise update the cursor and attempt the next window
		twbs.cursor = cursorEnd
	}
	return nil, ""
}

func (twbs *timeWindowBlockSelector) windowForBlock(meta *backend.BlockMeta) int64 {
	return meta.StartTime.Unix() / int64(twbs.MaxCompactionRange/time.Second)
}

type BlockMetaHeap []*backend.BlockMeta

func (h BlockMetaHeap) Len() int {
	return len(h)
}

func (h BlockMetaHeap) Less(i, j int) bool {
	return h[i].TotalObjects > h[j].TotalObjects
}

func (h BlockMetaHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *BlockMetaHeap) Push(x interface{}) {
	item := x.(*backend.BlockMeta)
	*h = append(*h, item)
}

func (h *BlockMetaHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[0 : n-1]
	return item
}
