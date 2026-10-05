package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/alexander-bachmann/errhance/internal/errhance"
	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

var rootCmd = newRootCmd()

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
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
			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			diff, err := cmd.Flags().GetBool("diff")
			if err != nil {
				return err
			}
			verbose, err := cmd.Flags().GetBool("verbose")
			if err != nil {
				return err
			}
			dryRun = dryRun || diff
			if diff {
				cmd.SilenceUsage = true
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
					fastwalk.WithSkipUnderscoreDirectories(true),
					fastwalk.WithOnlyFilepaths(filepaths),
				))
			srcCfg := errhance.NewSourceConfig(
				errhance.WithOverwrite(overwrite),
				errhance.WithGenerated(generated),
				errhance.WithDryRun(dryRun),
			)
			dirReport, err := errhance.LocalDirectoryContext(cmd.Context(), dirCfg, srcCfg)
			outputErr := writeDiagnostics(cmd.ErrOrStderr(), dirReport.Diagnostics, verbose)
			if diff && err == nil {
				root, cwdErr := os.Getwd()
				if cwdErr != nil {
					outputErr = errors.Join(outputErr, cwdErr)
				} else {
					outputErr = errors.Join(outputErr, writeDiff(cmd.OutOrStdout(), root, dirReport.Changes))
				}
			}
			filesModified, errorsEnhanced := dirReport.TotalFilesModified, dirReport.TotalErrorsEnhanced
			modified, enhanced := "modified", "enhanced"
			if dryRun {
				filesModified, errorsEnhanced = dirReport.ProposedFilesModified, dirReport.ProposedErrorsEnhanced
				modified, enhanced = "would be modified", "would be enhanced"
			}
			files := "files"
			errs := "errors"
			if filesModified == 1 {
				files = "file"
			}
			if errorsEnhanced == 1 {
				errs = "error"
			}
			out := cmd.OutOrStdout()
			if diff {
				out = cmd.ErrOrStderr()
			}
			_, writeErr := fmt.Fprintf(out, "%d %s %s, %d %s %s\n", filesModified, files, modified, errorsEnhanced, errs, enhanced)
			outputErr = errors.Join(outputErr, writeErr)
			if err != nil {
				return errors.Join(fmt.Errorf("errhance.LocalDirectory: %w", err), outputErr)
			}
			return outputErr
		},
	}
	rootCmd.Flags().BoolP("overwrite", "o", false, "also prefix supported fmt.Errorf calls, preserving their format and arguments")
	rootCmd.Flags().BoolP("generated", "g", false, "enhances returns in generated code")
	rootCmd.Flags().BoolP("vendor", "v", false, "enhances returns in vendor directory")
	rootCmd.Flags().StringSlice("filepaths", []string{}, "enhances specific filepaths")
	rootCmd.Flags().Bool("dry-run", false, "validate and report proposed changes without writing source files")
	rootCmd.Flags().Bool("diff", false, "print a unified diff without writing source files (implies --dry-run)")
	rootCmd.Flags().Bool("verbose", false, "print every diagnostic with its full position instead of grouping by message")
	return rootCmd
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}
