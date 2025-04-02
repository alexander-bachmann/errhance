package cmd

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/alexander-bachmann/errhance/internal/errhance"
	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "errhance",
	Short: "errhance recursively traverses a Go project, enhances returned errors, and writes the changes. The errors are enhanced by wrapping them with the name of the function that initially returned the error.",
	Long: `errhance recursively traverses a Go project, enhances returned errors, and writes the changes. The errors are enhanced by wrapping them with the name of the function that initially returned the error.

-- Before --
if err := foo.Bar(); err != nil {
    return err
}
-- After --
if err := foo.Bar(); err != nil {
    return fmt.Errorf("foo.Bar: %w", err)
}
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		overwrite, err := cmd.Flags().GetBool("overwrite")
		if err != nil {
			return err // noeh
		}
		forceOverwrite, err := cmd.Flags().GetBool("force-overwrite")
		if err != nil {
			return err // noeh
		}
		generated, err := cmd.Flags().GetBool("generated")
		if err != nil {
			return err // noeh
		}
		vendor, err := cmd.Flags().GetBool("vendor")
		if err != nil {
			return err // noeh
		}
		filepaths, err := cmd.Flags().GetStringSlice("filepaths")
		if err != nil {
			return err // noeh
		}
		var totalFilesModified atomic.Int64
		var totalErrorsEnhanced atomic.Int64
		//  closure for counting and for satisfying
		// the workFunc signature in fastwalk.Config
		errhanceFileWithArgs := func(_ context.Context, path string) error {
			report, err := errhanceFile(path, errhance.NewConfig(
				errhance.WithOverwrite(overwrite),
				errhance.WithForceOverwrite(forceOverwrite),
				errhance.WithGenerated(generated),
			))
			if report.TotalEnhanced > 0 {
				totalFilesModified.Add(1)
			}
			totalErrorsEnhanced.Add(int64(report.TotalEnhanced))
			return err // noeh
		}
		skipDirectories := []string{"vendor"}
		if vendor {
			skipDirectories = []string{}
		}
		err = errhanceFiles(
			fastwalk.NewConfig(
				errhanceFileWithArgs,
				fastwalk.WithOnlyExtensions([]string{".go"}),
				fastwalk.WithSkipDirectories(skipDirectories),
				fastwalk.WithOnlyFilepaths(filepaths),
				// TODO: add support for changing the remaining fastwalk opts as args
			))
		if err != nil {
			return err // noeh
		}
		files := "files"
		errs := "errors"
		if totalFilesModified.Load() == 1 {
			files = "file"
		}
		if totalErrorsEnhanced.Load() == 1 {
			errs = "error"
		}
		cmd.Printf("%d %s modified, %d %s enhanced\n",
			totalFilesModified.Load(),
			files,
			totalErrorsEnhanced.Load(),
			errs,
		)
		return nil
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
	rootCmd.Flags().BoolP("vendor", "v", false, "enhances returns in vendor directory")
	rootCmd.Flags().StringSlice("filepaths", []string{}, "enhances specific filepaths")
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
