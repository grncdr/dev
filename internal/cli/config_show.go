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
	UserConfig    *config.UserConfig    `json:"user_config"`
	Paths         showPaths             `json:"paths"`
}

type showPaths struct {
	ProjectConfig     string `json:"project_config"`
	LocalOverride     string `json:"local_override"`
	LocalOverrideUsed bool   `json:"local_override_used"`
	UserConfig        string `json:"user_config"`
	UserConfigFound   bool   `json:"user_config_found"`
}

func runConfigShow(opts *Options, output string, out io.Writer) error {
	if output != "text" && output != "json" {
		return fmt.Errorf("unknown output format: %s", output)
	}

	projectCfg, projectInfo, err := config.LoadProjectConfig(opts.ResolvedPaths.ProjectConfig)
	if err != nil {
		return err
	}

	userCfg, userInfo, err := config.LoadUserConfig(opts.ResolvedPaths.UserConfig)
	if err != nil {
		return err
	}

	if output == "json" {
		payload := showPayload{
			ProjectConfig: projectCfg,
			UserConfig:    userCfg,
			Paths: showPaths{
				ProjectConfig:     projectInfo.ConfigPath,
				LocalOverride:     projectInfo.LocalOverridePath,
				LocalOverrideUsed: projectInfo.LocalOverrideUsed,
				UserConfig:        userInfo.UserConfigPath,
				UserConfigFound:   userInfo.UserConfigFound,
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

	fmt.Fprintf(out, "User config: %s\n", userInfo.UserConfigPath)
	if !userInfo.UserConfigFound {
		fmt.Fprintln(out, "User config not found; using empty config")
	}
	userBytes, err := toml.Marshal(userCfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(userBytes))

	return nil
}
