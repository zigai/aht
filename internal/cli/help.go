package cli

import (
	urfavehelp "github.com/zigai/urfave-help"
)

const helpArgumentsKey = urfavehelp.LegacyArgsMetadataKey

// HelpArg documents a positional command argument.
type HelpArg = urfavehelp.Arg
