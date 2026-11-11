package statecache

// Fixed logical blocks keep inventory growth from copying millions of live
// reflect.Values. Only the first small block grows; later blocks allocate once.
// Blocks are an encoder allocation detail. Discovery order and wire IDs stay
// consecutive, independent of block boundaries.
const nodeBlockShift = 11
const nodeBlockSize = 1 << nodeBlockShift

type nodeInventory struct {
	blocks [][]stateNode
	count  int
}

func (s *nodeInventory) add(node stateNode) {
	index := s.count >> nodeBlockShift
	offset := s.count & (nodeBlockSize - 1)
	if index == len(s.blocks) {
		size := nodeBlockSize
		if index == 0 {
			size = 8
		}
		s.blocks = append(s.blocks, make([]stateNode, size))
	}
	block := s.blocks[index]
	if offset == len(block) {
		grown := make([]stateNode, len(block)*2)
		copy(grown, block)
		s.blocks[index] = grown
		block = grown
	}
	block[offset] = node
	s.count++
}

func (s *nodeInventory) at(index int) *stateNode {
	return &s.blocks[index>>nodeBlockShift][index&(nodeBlockSize-1)]
}
