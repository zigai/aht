package manage

import "testing"

func TestSameTmuxServerDoesNotTreatMissingIdentityAsWildcard(t *testing.T) {
	t.Parallel()
	if !sameTmuxServer("", "default") || sameTmuxServer("-L:work", "") || sameTmuxServer("-L:work", "-L:other") {
		t.Fatal("tmux server matching was not conservative")
	}
}
