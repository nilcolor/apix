package main

import (
	"fmt"
	"os"

	flags "github.com/jessevdk/go-flags"
)

// revision is set at build time via -ldflags "-X main.revision=..."
var revision = "dev"

type globalOptions struct {
	Version func() `long:"version" description:"Print version and exit"`
	Help    func() `short:"h" long:"help" description:"Show this help message"`
}

type InvokeCommand struct {
	Var      []string `long:"var"       description:"Set or override a variable (key=value, repeatable)"`
	Env      string   `long:"env"       description:"Load variables from a .env file"`
	Step     []string `long:"step"      description:"Run only this step (repeatable / comma-separated)"`
	From     string   `long:"from"      description:"Start execution at this step"`
	Skip     []string `long:"skip"      description:"Skip this step (repeatable)"`
	DryRun   bool     `long:"dry-run"   description:"Print resolved requests without executing"`
	Verbose  bool     `long:"verbose"   short:"v" description:"Show full request/response details"`
	Output   string   `long:"output"    short:"o" choice:"pretty" choice:"json" choice:"silent" default:"pretty" description:"Output format"`
	NoColor  bool     `long:"no-color"  description:"Disable ANSI color output"`
	Timeout  string   `long:"timeout"   description:"Override global timeout (e.g. 10s, 1m)"`
	FailFast bool     `long:"fail-fast" description:"Stop on first failure"`
	Args     struct {
		File string `positional-arg-name:"scroll" required:"yes" description:"Scroll (YAML request file) to invoke"`
	} `positional-args:"yes"`
}

func main() {
	global := &globalOptions{}
	global.Version = func() {
		fmt.Printf("apix %s\n", revision)
		os.Exit(0)
	}

	// Remove the built-in help flag so we can prepend a banner.
	parser := flags.NewParser(global, flags.Default&^flags.HelpFlag)

	global.Help = func() {
		fmt.Printf("apix — execute YAML-defined HTTP request sequences\n")
		fmt.Printf("version: %s\n\n", revision)
		parser.WriteHelp(os.Stdout)
		os.Exit(0)
	}

	_, err := parser.AddCommand("invoke", "Invoke a scroll (YAML request file)", "", &InvokeCommand{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}
}
