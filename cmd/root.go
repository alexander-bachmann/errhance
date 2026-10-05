package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/alexander-bachmann/errhance/internal/errhance"
	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

var rootCmd = &cobra.Command{
	Use:   "errhance",
	Args:  cobra.NoArgs,
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
		skipDirectories := []string{"testdata", "vendor"}
		if vendor {
			skipDirectories = []string{"testdata"}
		}
		emptyWorkFunc := func(_ context.Context, _ string) error { return nil }
		dirCfg := errhance.NewDirectoryConfig(
			fastwalk.NewConfig(
				emptyWorkFunc, // overwritten in errhance.LocalDirectory
				fastwalk.WithOnlyExtensions([]string{".go"}),
				fastwalk.WithSkipDirectories(skipDirectories),
				fastwalk.WithOnlyFilepaths(filepaths),
			))
		srcCfg := errhance.NewSourceConfig(
			errhance.WithOverwrite(overwrite),
			errhance.WithGenerated(generated),
		)
		dirReport, err := errhance.LocalDirectoryContext(cmd.Context(), dirCfg, srcCfg)
		for _, diagnostic := range dirReport.Diagnostics {
			if diagnostic.Position.IsValid() || diagnostic.Position.Filename != "" {
				cmd.PrintErrf("%s: %s\n", diagnostic.Position, diagnostic.Message)
			} else {
				cmd.PrintErrln(diagnostic.Message)
			}
		}
		files := "files"
		errs := "errors"
		if dirReport.TotalFilesModified == 1 {
			files = "file"
		}
		if dirReport.TotalErrorsEnhanced == 1 {
			errs = "error"
		}
		cmd.Printf("%d %s modified, %d %s enhanced\n",
			dirReport.TotalFilesModified,
			files,
			dirReport.TotalErrorsEnhanced,
			errs,
		)
		if err != nil {
			return fmt.Errorf("errhance.LocalDirectory: %w", err)
		}
		return nil
	},
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolP("overwrite", "o", false, "also prefix supported fmt.Errorf calls, preserving their format and arguments")
	rootCmd.Flags().BoolP("generated", "g", false, "enhances returns in generated code")
	rootCmd.Flags().BoolP("vendor", "v", false, "enhances returns in vendor directory")
	rootCmd.Flags().StringSlice("filepaths", []string{}, "enhances specific filepaths")
}
