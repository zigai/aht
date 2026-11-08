package processinfo

import "context"

type Scanner struct {
	processes []Process
}

func (s *Scanner) List(
	ctx context.Context,
	keys []string,
	includeEnv, _ func(Process) bool,
) ([]Process, error) {
	processes, err := ListWithEnv(ctx, keys, includeEnv)
	if err != nil {
		return nil, err
	}
	s.processes = processes
	return processes, nil
}

func (s *Scanner) Details(_ context.Context, pid int) (Process, bool, error) {
	for _, process := range s.processes {
		if process.PID == pid {
			return process, true, nil
		}
	}
	return Process{}, false, nil
}

func (s *Scanner) Close() { s.processes = nil }
