package semantic

import "reflect"

// Branch environments of the return-borrow walk.
//
// Every branch (if/elif/else, match arm, loop body, block statement, scoped condition binder) used
// to walk a fresh CLONE of the alias environment and join the clone back afterwards. On a large
// function that copied every local's entry at every branch, at every walk: ~6% of compiling the
// stage1 driver. A branch now walks the parent map IN PLACE inside a journal frame; closing the
// frame reports the entries the branch wrote (their final values) and undoes those writes, so the
// next branch starts from the same environment the clone used to give it. The caller then joins
// the reported entries, exactly as the old clone-and-merge did: entries a branch never wrote merged
// with themselves, which is a no-op.
//
// Every write to an alias map goes through journalReturnBorrowAlias first. Only the innermost
// frame's map is journaled: a callee summarized mid-branch walks its own map in its own frames.

type returnBorrowAliasJournalEntry struct {
	name    string
	old     returnBorrowFlow
	existed bool
}

type returnBorrowAliasFrame struct {
	aliases unsafeMapID
	start   int
}

type unsafeMapID = uintptr

// returnBorrowBranchChange is one entry a branch wrote: its final value, and whether the name was
// bound before the branch began.
type returnBorrowBranchChange struct {
	name          string
	flow          returnBorrowFlow
	existedBefore bool
}

func returnBorrowAliasMapID(aliases map[string]returnBorrowFlow) unsafeMapID {
	return uintptr(reflect.ValueOf(aliases).UnsafePointer())
}

func (a *Analyzer) journalReturnBorrowAlias(aliases map[string]returnBorrowFlow, name string) {
	n := len(a.returnBorrowAliasFrames)
	if n == 0 || aliases == nil || a.returnBorrowAliasFrames[n-1].aliases != returnBorrowAliasMapID(aliases) {
		return
	}
	old, existed := aliases[name]
	a.returnBorrowAliasJournal = append(a.returnBorrowAliasJournal, returnBorrowAliasJournalEntry{name: name, old: old, existed: existed})
}

// writeReturnBorrowAlias is the journaled `aliases[name] = flow`.
func (a *Analyzer) writeReturnBorrowAlias(aliases map[string]returnBorrowFlow, name string, flow returnBorrowFlow) {
	a.journalReturnBorrowAlias(aliases, name)
	aliases[name] = flow
}

// beginReturnBorrowBranch opens a frame on aliases and returns the map the branch walks: aliases
// itself, or -- for a nil environment (a value walked outside a body) -- a fresh scratch map, as
// cloning nil used to give. A scratch map's writes are discarded: nothing joins into nil.
func (a *Analyzer) beginReturnBorrowBranch(aliases map[string]returnBorrowFlow) map[string]returnBorrowFlow {
	if aliases == nil {
		return map[string]returnBorrowFlow{}
	}
	a.returnBorrowAliasFrames = append(a.returnBorrowAliasFrames, returnBorrowAliasFrame{
		aliases: returnBorrowAliasMapID(aliases),
		start:   len(a.returnBorrowAliasJournal),
	})
	return aliases
}

// endReturnBorrowBranch closes the frame beginReturnBorrowBranch(aliases) opened: it returns the entries written since it opened,
// in first-write order, and restores aliases to its state at the open.
func (a *Analyzer) endReturnBorrowBranch(aliases map[string]returnBorrowFlow) []returnBorrowBranchChange {
	if aliases == nil {
		return nil
	}
	n := len(a.returnBorrowAliasFrames)
	frame := a.returnBorrowAliasFrames[n-1]
	if frame.aliases != returnBorrowAliasMapID(aliases) {
		panic("return-borrow alias journal: frames closed out of order")
	}
	a.returnBorrowAliasFrames = a.returnBorrowAliasFrames[:n-1]
	entries := a.returnBorrowAliasJournal[frame.start:]
	var changes []returnBorrowBranchChange
	if len(entries) != 0 {
		// Most frames write a handful of names: dedupe by scanning the changes so far, and only
		// build a set for a large frame.
		var written map[string]bool
		if len(entries) > 16 {
			written = make(map[string]bool, len(entries))
		}
		for index, entry := range entries {
			if written != nil {
				if written[entry.name] {
					continue
				}
				written[entry.name] = true
			} else if returnBorrowJournalNameBefore(entries[:index], entry.name) {
				continue
			}
			if flow, ok := aliases[entry.name]; ok {
				if changes == nil {
					// At most one change per remaining entry: size once instead of regrowing.
					changes = make([]returnBorrowBranchChange, 0, len(entries)-index)
				}
				changes = append(changes, returnBorrowBranchChange{name: entry.name, flow: flow, existedBefore: entry.existed})
			}
		}
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			if entry.existed {
				aliases[entry.name] = entry.old
			} else {
				delete(aliases, entry.name)
			}
		}
	}
	clear(entries)
	a.returnBorrowAliasJournal = a.returnBorrowAliasJournal[:frame.start]
	return changes
}

// joinReturnBorrowBranch merges a closed branch's writes into aliases (the old
// mergeReturnBorrowAliasMaps of the branch's clone), skipping names skip reports.
func (a *Analyzer) joinReturnBorrowBranch(aliases map[string]returnBorrowFlow, changes []returnBorrowBranchChange, skip func(returnBorrowBranchChange) bool) {
	if aliases == nil {
		return
	}
	for _, change := range changes {
		if skip != nil && skip(change) {
			continue
		}
		a.writeReturnBorrowAlias(aliases, change.name, mergeReturnBorrowFlow(aliases[change.name], change.flow))
	}
}

func returnBorrowJournalNameBefore(entries []returnBorrowAliasJournalEntry, name string) bool {
	for _, entry := range entries {
		if entry.name == name {
			return true
		}
	}
	return false
}
