package tmux

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

var errInvalidDarwinProcArgs = errors.New("invalid Darwin process arguments")

// parseDarwinProcArgs decodes the kern.procargs2 layout: native argc, the
// executable path, padding, and then argc NUL-delimited argv entries. Current
// Darwin architectures are little-endian.
func parseDarwinProcArgs(data []byte) ([]string, error) {
	const argcSize = 4
	if len(data) < argcSize {
		return nil, errInvalidDarwinProcArgs
	}

	rawArgc := binary.LittleEndian.Uint32(data[:argcSize])
	if rawArgc == 0 || rawArgc > math.MaxInt32 {
		return nil, errInvalidDarwinProcArgs
	}

	argc := int(rawArgc)
	if argc > len(data)-argcSize {
		return nil, errInvalidDarwinProcArgs
	}

	payload := data[argcSize:]

	executableEnd := bytes.IndexByte(payload, 0)
	if executableEnd < 0 {
		return nil, errInvalidDarwinProcArgs
	}

	payload = payload[executableEnd+1:]
	for len(payload) > 0 && payload[0] == 0 {
		payload = payload[1:]
	}

	args := make([]string, 0, argc)
	for len(args) < argc {
		end := bytes.IndexByte(payload, 0)
		if end < 0 {
			return nil, errInvalidDarwinProcArgs
		}

		args = append(args, string(payload[:end]))
		payload = payload[end+1:]
	}

	return args, nil
}
