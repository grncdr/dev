package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"dev-mode/internal/config"
)

func newConfigCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "inspect config",
	}

	cmd.AddCommand(newConfigShowCmd(opts))
	return cmd
}

func newConfigShowCmd(opts *Options) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "show effective config",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigShow(opts, output, os.Stdout)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	return cmd
}

type showPayload struct {
	ProjectConfig *config.ProjectConfig `json:"project_config"`
	DaemonConfig  *config.DaemonConfig  `json:"daemon_config"`
	Paths         showPaths             `json:"paths"`
}

type showPaths struct {
	ProjectConfig     string `json:"project_config"`
	LocalOverride     string `json:"local_override"`
	LocalOverrideUsed bool   `json:"local_override_used"`
	DaemonConfig      string `json:"daemon_config"`
	DaemonConfigFound bool   `json:"daemon_config_found"`
}

func runConfigShow(opts *Options, output string, out io.Writer) error {
	if output != "text" && output != "json" {
		return fmt.Errorf("unknown output format: %s", output)
	}

	projectCfg, projectInfo, err := config.LoadProjectConfig(opts.ResolvedPaths.ProjectConfig)
	if err != nil {
		return err
	}

	daemonCfg, daemonInfo, err := config.LoadDaemonConfig(opts.ResolvedPaths.DaemonConfig)
	if err != nil {
		return err
	}

	if output == "json" {
		payload := showPayload{
			ProjectConfig: projectCfg,
			DaemonConfig:  daemonCfg,
			Paths: showPaths{
				ProjectConfig:     projectInfo.ConfigPath,
				LocalOverride:     projectInfo.LocalOverridePath,
				LocalOverrideUsed: projectInfo.LocalOverrideUsed,
				DaemonConfig:      daemonInfo.DaemonConfigPath,
				DaemonConfigFound: daemonInfo.DaemonConfigFound,
			},
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	}

	fmt.Fprintf(out, "Project config: %s\n", projectInfo.ConfigPath)
	if projectInfo.LocalOverridePath != "" {
		if projectInfo.LocalOverrideUsed {
			fmt.Fprintf(out, "Local override: %s (applied)\n", projectInfo.LocalOverridePath)
		} else {
			fmt.Fprintf(out, "Local override: %s (not found)\n", projectInfo.LocalOverridePath)
		}
	}

	projectBytes, err := toml.Marshal(projectCfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(projectBytes))

	fmt.Fprintf(out, "Daemon config: %s\n", daemonInfo.DaemonConfigPath)
	if !daemonInfo.DaemonConfigFound {
		fmt.Fprintln(out, "Daemon config not found; using empty config")
	}
	daemonBytes, err := toml.Marshal(daemonCfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(daemonBytes))

	return nil
}
