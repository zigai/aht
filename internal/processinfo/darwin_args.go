package processinfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

var errInvalidDarwinProcArgs = errors.New("invalid Darwin process arguments")

// parseDarwinProcArgs decodes the kern.procargs2 layout: native argc, the
// executable path, padding, argc NUL-delimited argv entries, then environment. Current
// Darwin architectures are little-endian.
func parseDarwinProcArgs(data []byte) (string, []string, []byte, error) {
	const argcSize = 4
	if len(data) < argcSize {
		return "", nil, nil, errInvalidDarwinProcArgs
	}

	rawArgc := binary.LittleEndian.Uint32(data[:argcSize])
	if rawArgc == 0 || rawArgc > math.MaxInt32 {
		return "", nil, nil, errInvalidDarwinProcArgs
	}

	argc := int(rawArgc)
	if argc > len(data)-argcSize {
		return "", nil, nil, errInvalidDarwinProcArgs
	}

	payload := data[argcSize:]

	executableEnd := bytes.IndexByte(payload, 0)
	if executableEnd < 0 {
		return "", nil, nil, errInvalidDarwinProcArgs
	}

	executable := string(payload[:executableEnd])

	payload = payload[executableEnd+1:]
	for len(payload) > 0 && payload[0] == 0 {
		payload = payload[1:]
	}

	args := make([]string, 0, argc)
	for len(args) < argc {
		end := bytes.IndexByte(payload, 0)
		if end < 0 {
			return "", nil, nil, errInvalidDarwinProcArgs
		}

		args = append(args, string(payload[:end]))
		payload = payload[end+1:]
	}

	return executable, args, payload, nil
}
