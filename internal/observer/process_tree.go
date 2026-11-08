package observer

import (
	"iter"

	"github.com/zigai/aht/v2/internal/processinfo"
)

type processTree struct {
	byPID     map[int]processinfo.Process
	ancestors map[int][]int
}

func newProcessTree(byPID map[int]processinfo.Process) *processTree {
	return &processTree{byPID: byPID, ancestors: make(map[int][]int)}
}

func (tree *processTree) parents(pid int) iter.Seq[int] {
	return func(yield func(int) bool) {
		ancestors, known := tree.ancestors[pid]
		if !known {
			seen := map[int]bool{pid: true}
			current, ok := tree.byPID[pid]

			for ok && current.PPID > 0 && !seen[current.PPID] {
				seen[current.PPID] = true
				ancestors = append(ancestors, current.PPID)
				current, ok = tree.byPID[current.PPID]
			}

			tree.ancestors[pid] = ancestors
		}

		for _, parent := range ancestors {
			if !yield(parent) {
				return
			}
		}
	}
}

func (tree *processTree) descendsFrom(pid int, root int) bool {
	if root <= 0 {
		return false
	}

	for parent := range tree.parents(pid) {
		if parent == root {
			return true
		}
	}

	return false
}
