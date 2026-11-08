package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/config"
)

const (
	configSetArgsCount = 2
	configGetArgsCount = 1
)

var (
	errNoConfigDisallowsInit    = errors.New("cannot initialize config when --no-config is set")
	errInitJSONTemplateConflict = errors.New("cannot output JSON when writing template to stdout")
	errNoConfigDisallowsSet     = errors.New("cannot set config when --no-config is set")
	errUnknownConfigKey         = errors.New("unknown configuration key")
)

type configKeyOrigin struct {
	Source   string `json:"source"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	RawValue string `json:"raw_value,omitempty"`
}

type configProvenance struct {
	Config      config.Config              `json:"config"`
	ActiveFiles []string                   `json:"active_files"`
	Origins     map[string]configKeyOrigin `json:"origins"`
}

func (app *application) loadConfig() (config.Config, error) {
	if app.cfgLoaded {
		return app.cfg, app.cfgErr
	}

	cfg, meta, resolved, err := config.LoadWithMetadata(config.Options{
		Path:     app.configPath,
		Explicit: app.configPath != "",
		NoConfig: app.noConfig,
		Stdin:    app.stdin,
	})
	app.cfg = cfg
	app.cfgMeta = meta
	app.resolvedConfigPath = resolved
	app.cfgErr = exitCode(err, exitCodeUsage)
	app.cfgLoaded = true

	return app.cfg, app.cfgErr
}

func (app *application) newManageConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "config",
		Short:         "Inspect configuration path and effective settings",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(
		app.newConfigPathCommand(),
		app.newConfigShowCommand(),
		app.newConfigGetCommand(),
		app.newConfigSetCommand(),
		app.newConfigInitCommand(),
		app.newConfigSchemaCommand(),
	)

	return command
}

func (app *application) newConfigPathCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "path",
		Short:         "Print resolved configuration file path",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			path := app.resolvedConfigPath
			if path == "" {
				path = config.DefaultPath()
			}

			if app.outputJSON {
				return app.writeJSON(map[string]string{"path": path})
			}

			return app.writef("%s\n", path)
		},
	}
}

func (app *application) newConfigShowCommand() *cobra.Command {
	var showProvenance bool

	cmd := &cobra.Command{
		Use:           "show",
		Short:         "Print effective configuration",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			return err
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runConfigShow(showProvenance)
		},
	}
	cmd.Flags().
		BoolVar(&showProvenance, "provenance", false, "include layer provenance for resolved keys")

	return cmd
}

func (app *application) runConfigShow(showProvenance bool) error {
	if app.outputJSON {
		if showProvenance && app.cfgMeta != nil {
			return app.writeJSON(app.configWithProvenance())
		}

		return app.writeJSON(app.cfg)
	}

	var b strings.Builder

	if showProvenance && app.cfgMeta != nil {
		app.appendConfigProvenance(&b)
	}

	data, err := toml.Marshal(app.cfg)
	if err != nil {
		return fmt.Errorf("encode config toml: %w", err)
	}

	b.Write(data)

	return app.writef("%s", b.String())
}

func (app *application) configWithProvenance() configProvenance {
	origins := make(map[string]configKeyOrigin)

	for _, key := range config.Keys() {
		if origin, ok := app.cfgMeta.Where(key); ok {
			origins[key] = configKeyOrigin{
				Source:   string(origin.Source),
				Path:     origin.Path,
				Line:     origin.Line,
				RawValue: origin.RawValue,
			}
		}
	}

	return configProvenance{
		Config:      app.cfg,
		ActiveFiles: app.cfgMeta.ActiveFiles(),
		Origins:     origins,
	}
}

func (app *application) appendConfigProvenance(b *strings.Builder) {
	active := app.cfgMeta.ActiveFiles()
	if len(active) > 0 {
		fmt.Fprintf(b, "# Active configuration files (highest precedence last):\n")

		for _, file := range active {
			fmt.Fprintf(b, "#   - %s\n", file)
		}

		fmt.Fprintf(b, "\n")
	}

	for _, key := range config.Keys() {
		if origin, ok := app.cfgMeta.Where(key); ok {
			fmt.Fprintf(b, "# %s (from %s)\n", key, configOriginLabel(origin))
		}
	}

	fmt.Fprintf(b, "\n")
}

func (app *application) newConfigGetCommand() *cobra.Command {
	var showProvenance bool

	cmd := &cobra.Command{
		Use:           "get <key>",
		Short:         "Get a configuration setting by key",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(configGetArgsCount),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			return err
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return app.runConfigGet(args[0], showProvenance)
		},
	}
	cmd.Flags().
		BoolVar(&showProvenance, "provenance", false, "include layer provenance for the key")

	return cmd
}

func (app *application) runConfigGet(key string, showProvenance bool) error {
	val, ok := app.cfg.Get(key)
	if !ok {
		return exitCode(fmt.Errorf("%w: %q", errUnknownConfigKey, key), exitCodeUsage)
	}

	if app.outputJSON {
		return app.writeConfigValueJSON(key, val, showProvenance)
	}

	if showProvenance && app.cfgMeta != nil {
		if origin, found := app.cfgMeta.Where(key); found {
			return app.writef("%v (from %s)\n", val, configOriginLabel(origin))
		}
	}

	return app.writef("%v\n", val)
}

func (app *application) writeConfigValueJSON(key string, val any, showProvenance bool) error {
	result := map[string]any{"key": key, "value": val}
	if showProvenance && app.cfgMeta != nil {
		if origin, found := app.cfgMeta.Where(key); found {
			result["origin"] = map[string]any{
				"source": string(origin.Source), "path": origin.Path,
				"line": origin.Line, "raw_value": origin.RawValue,
			}
		}
	}

	return app.writeJSON(result)
}

func configOriginLabel(origin strata.Origin) string {
	switch {
	case origin.Line > 0:
		return fmt.Sprintf("%s:%d", origin.Path, origin.Line)
	case origin.Path != "":
		return fmt.Sprintf("%s %s", origin.Source, origin.Path)
	default:
		return string(origin.Source)
	}
}

func (app *application) newConfigSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "schema",
		Short:         "Output JSON Schema for AHT configuration",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.writef("%s", config.Schema)
		},
	}
}

func (app *application) newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "set <key> <value>",
		Short:         "Set a configuration key in the active config file",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(configSetArgsCount),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if app.noConfig {
				return exitCode(errNoConfigDisallowsSet, exitCodeUsage)
			}

			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			key, val := args[0], args[1]

			path, err := config.EditPath(app.configPath)
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}

			if err := config.Set(path, key, val); err != nil {
				if errors.Is(err, config.ErrInvalidSetting) {
					return exitCode(err, exitCodeUsage)
				}

				return fmt.Errorf("setting config: %w", err)
			}

			if app.outputJSON {
				return app.writeJSON(map[string]any{
					"path":  path,
					"key":   key,
					"value": val,
					"set":   true,
				})
			}

			return app.writef("set %s = %s in %s\n", key, val, path)
		},
	}
}

func (app *application) newConfigInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Initialize default configuration file",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runConfigInit(force)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing configuration file")

	return cmd
}

func (app *application) runConfigInit(force bool) error {
	if app.noConfig {
		return exitCode(errNoConfigDisallowsInit, exitCodeUsage)
	}

	if app.configPath == "-" && app.outputJSON {
		return exitCode(errInitJSONTemplateConflict, exitCodeUsage)
	}

	if app.configPath == "-" {
		return app.writef("%s", config.DefaultConfigTemplate())
	}

	path, err := config.EditPath(app.configPath)
	if err != nil {
		return exitCode(err, exitCodeUsage)
	}

	exists, err := config.FileExists(path)
	if err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	if exists && !force {
		return app.reportConfigInit(path, false)
	}

	if err := config.WriteConfigFile(path); err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	return app.reportConfigInit(path, true)
}

func (app *application) reportConfigInit(path string, created bool) error {
	if !created {
		if app.outputJSON {
			return app.writeJSON(map[string]any{
				"created": false, "path": path,
				"message": "config file already exists (use --force to overwrite)",
			})
		}

		app.warnf("config file already exists at %s (use --force to overwrite)\n", path)

		return nil
	}

	if app.outputJSON {
		return app.writeJSON(map[string]any{"created": true, "path": path})
	}

	app.warnf("created %s\n", path)

	return nil
}
