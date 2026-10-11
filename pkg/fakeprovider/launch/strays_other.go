//go:build !linux

package launch

func (*Harness) stopStrays() error {
	return nil
}
