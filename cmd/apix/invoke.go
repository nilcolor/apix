package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/joho/godotenv"

	"github.com/nilcolor/apix/internal/loader"
	"github.com/nilcolor/apix/internal/output"
	"github.com/nilcolor/apix/internal/pipeline"
	"github.com/nilcolor/apix/internal/schema"
	"github.com/nilcolor/apix/internal/vars"
)

func (r *InvokeCommand) Execute(_ []string) error {
	os.Exit(invokeCmd(r, os.Stdout, os.Stderr))
	return nil // unreachable
}

// invokeCmd executes the run command and returns an exit code.
// Separated from Execute so integration tests can call it without os.Exit.
func invokeCmd(r *InvokeCommand, stdout, stderr io.Writer) int {
	if r.NoColor {
		color.NoColor = true
	}

	// --env: load dotenv file into os environment before BuildStore.
	if r.Env != "" {
		if err := godotenv.Load(r.Env); err != nil {
			fmt.Fprintf(stderr, "error: load env %q: %v\n", r.Env, err)
			return 2
		}
	}

	// --var: parse key=value pairs.
	cliVars, err := parseVars(r.Var)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	// --timeout: parse duration; zero means "use file config".
	var timeout time.Duration
	if r.Timeout != "" {
		d, parseErr := time.ParseDuration(r.Timeout)
		if parseErr != nil {
			fmt.Fprintf(stderr, "error: invalid --timeout %q: %v\n", r.Timeout, parseErr)
			return 2
		}
		timeout = d
	}

	// Load and resolve the request file (includes, config merge).
	file, err := loader.Load(r.Args.File)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	// Override timeout if --timeout was supplied.
	if timeout > 0 {
		file.Config.Timeout = schema.Duration{Duration: timeout}
	}

	// Build variable store (priority: CLI > file > included).
	store, err := vars.BuildStore(file, cliVars)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	opts := pipeline.Options{
		DryRun:   r.DryRun,
		FailFast: r.FailFast,
		Step:     r.Step,
		Skip:     r.Skip,
		From:     r.From,
		Stderr:   stderr,
	}

	results, summary, err := pipeline.Run(file.Steps, &file.Config, store, opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	// Route output: JSON → stdout (pipe-friendly), pretty/verbose → stderr, silent → nowhere.
	// In all non-JSON modes, print: values go to stdout so they can be piped independently.
	switch r.Output {
	case "json":
		if err := output.JSON(results, summary, stdout); err != nil {
			fmt.Fprintf(stderr, "error: write JSON output: %v\n", err)
			return 2
		}
	case "silent":
		for i := range results {
			if results[i].Printed != "" {
				fmt.Fprintln(stdout, results[i].Printed)
			}
		}
	default: // "pretty"
		if r.Verbose {
			output.PrettyVerbose(results, summary, stderr, stdout)
		} else {
			output.Pretty(results, summary, stderr, stdout)
		}
	}

	return exitCode(results)
}

// parseVars converts ["key=value", ...] to a map. Returns an error on malformed entries.
func parseVars(entries []string) (map[string]string, error) {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		idx := strings.IndexByte(e, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("malformed --var %q: expected key=value", e)
		}
		m[e[:idx]] = e[idx+1:]
	}
	return m, nil
}

// exitCode derives the process exit code from per-step results.
// Any step execution error → 2; any failed assertion → 1; all pass → 0.
func exitCode(results []output.StepResult) int {
	for i := range results {
		if results[i].Error != "" {
			return 2
		}
	}
	for i := range results {
		for _, a := range results[i].Assertions {
			if !a.Passed {
				return 1
			}
		}
	}
	return 0
}
