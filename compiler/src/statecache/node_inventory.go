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
	if index == len(s.blocks) {
		capacity := nodeBlockSize
		if index == 0 {
			capacity = 8
		}
		s.blocks = append(s.blocks, make([]stateNode, 0, capacity))
	}
	block := s.blocks[index]
	if len(block) == cap(block) {
		grown := make([]stateNode, len(block), min(cap(block)*2, nodeBlockSize))
		copy(grown, block)
		block = grown
	}
	s.blocks[index] = append(block, node)
	s.count++
}

func (s *nodeInventory) at(index int) *stateNode {
	return &s.blocks[index>>nodeBlockShift][index&(nodeBlockSize-1)]
}
