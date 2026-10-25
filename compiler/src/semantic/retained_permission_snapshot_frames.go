package semantic

// Saved rows retain the original five shallow slice headers, keyed exactly as
// the prior per-scope map. One frame belongs to each simultaneously active row
// scope. Frames never escape the invocation-local ordered inventory context.
type retainedPermissionSnapshotFrame struct {
	saved map[*FuncType]permissionRowSnapshot
}

func (inventory *retainedPermissionInventory) acquirePermissionSnapshots() *retainedPermissionSnapshotFrame {
	if inventory == nil {
		// Compatibility entry points without an explicit update context retain a
		// private one-shot frame. It must not borrow any other scope's buffer.
		return &retainedPermissionSnapshotFrame{saved: make(map[*FuncType]permissionRowSnapshot)}
	}
	depth := inventory.snapshotDepth
	if depth == len(inventory.snapshots) {
		inventory.snapshots = append(inventory.snapshots, &retainedPermissionSnapshotFrame{saved: make(map[*FuncType]permissionRowSnapshot)})
	}
	frame := inventory.snapshots[depth]
	if len(frame.saved) != 0 {
		panic("permission snapshot frame was not released")
	}
	inventory.snapshotDepth++
	return frame
}

func (inventory *retainedPermissionInventory) releasePermissionSnapshots(frame *retainedPermissionSnapshotFrame) {
	if inventory != nil {
		depth := inventory.snapshotDepth - 1
		if depth < 0 || inventory.snapshots[depth] != frame {
			panic("permission snapshot scopes released out of order")
		}
		inventory.snapshotDepth = depth
	}
	// Defer this even across panic/Goexit: references to FuncTypes and old slice
	// backings must not survive in idle scratch frames. This is buffer cleanup,
	// not a change to prior normal-return-only semantic row restoration.
	clear(frame.saved)
}
