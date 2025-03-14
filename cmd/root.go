package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/alexander-bachmann/errhance/internal/errhance"
	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "errhance",
	Short: "CLI that enhances the returned errors in a Go project by wrapping them with the name of the called function(s).",
	Long: `errhance recursively traverses a Go project, enhances returned errors, and writes the changes. The errors are enhanced by wrapping them with the name of the function that initially returned the error.

-- Before --
if err := foo.Bar(); err != nil {
    return err
}
-- After --
if err := foo.Bar(); err != nil {
    return fmt.Errorf("foo.Bar: %w", err)
}

Default Behavior:
- only 'return err' statements are enhanced; to enhance 'return fmt.Errorf' statements, use the flags: --overwrite or --force-overwrite
    - The --overwrite flag will enhance both 'return err' and safe 'return fmt.Errorf' statements
    - The --force-overwrite flag will enhance 'return err' , safe 'return fmt.Errorf', and unsafe 'fmt.Errorf' statements
    - Safe fmt.Errorf example:
        - return fmt.Errorf("failed: %w", err)
    - Unsafe fmt.Errorf examples:
        - return fmt.Errorf("failed: %w", ErrNotFound)    // sentinel error
        - return fmt.Errorf("failed: %w: %w", errA, errB) // multiple identifiers
        - return fmt.Errorf("%s: %w", str, err)           // multiple identifiers

- Generated code is not enhanced; to enhance generated code, use the flag: --generated

- Specific 'if err != nil' blocks are skipped if a comment is detected with any of the following: noeh, no eh, noerrhance, or no errhance
// ...
if err != nil {
    return err //noerrhance
}
// ...
if err != nil {
    return fmt.Errorf("custom message: %w", err) // noeh
}
	`,
	RunE: func(cmd *cobra.Command, args []string) error {
		overwrite, err := cmd.Flags().GetBool("overwrite")
		if err != nil {
			return err //noeh
		}
		forceOverwrite, err := cmd.Flags().GetBool("force-overwrite")
		if err != nil {
			return err //noeh
		}
		generated, err := cmd.Flags().GetBool("generated")
		if err != nil {
			return err //noeh
		}
		errhanceFileWithArgs := func(_ context.Context, path string) error {
			return errhanceFile(path, errhance.NewConfig(
				errhance.WithOverwrite(overwrite),
				errhance.WithForceOverwrite(forceOverwrite),
				errhance.WithGenerated(generated),
			))
		}
		return errhanceFiles(
			fastwalk.NewConfig(
				errhanceFileWithArgs,
				fastwalk.WithOnlyExtensions([]string{".go"}),
				// TODO: add support for changing the remaining fastwalk opts as args
			))
	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// uncomment to use viper
	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.errhance.yaml)")

	rootCmd.Flags().BoolP("overwrite", "o", false, "returns enhanced: err and safe fmt.Errorf")
	rootCmd.Flags().BoolP("force-overwrite", "f", false, "returns enhanced: err, safe fmt.Errorf, and unsafe fmt.Errorf")
	rootCmd.Flags().BoolP("generated", "g", false, "enhances returns in generated code")
}

func initConfig() {
	if cfgFile != "" {
		// use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// find home directory
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)
		// search config in home directory with name ".errhance" (without extension)
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".errhance")
	}
	viper.AutomaticEnv() // read in environment variables that match
	// if a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
