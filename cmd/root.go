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
	Long: `CLI that enhances the returned errors in a Go project by wrapping them with the name of the called function(s).

-- Before --
err := Foo()
if err != nil {
    return err
}
-- After --
err := Foo()
if err != nil {
    return fmt.Errorf("Foo: %w", err)
}`,
	RunE: func(cmd *cobra.Command, args []string) error {
		overwrite, err := cmd.Flags().GetBool("overwrite")
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
				errhance.WithGeneratedCode(generated),
				// TODO: create revert command
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

	rootCmd.Flags().BoolP("overwrite", "o", false, "errhances both 'return err' and 'return fmt.Errorf()'; useful for when functions are renamed")
	rootCmd.Flags().BoolP("generated", "g", false, "errhances generated code")
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
