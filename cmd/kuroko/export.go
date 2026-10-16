package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logstore"
	"github.com/ryu/kuroko/internal/viewer"
)

const exportUsage = "usage: kuroko export [--last | <logfile>] [--command N] [-o <file>]\nWithout --command, exports the entire session. Without -o, writes to stdout.\n"

func resolveLogArgument(args []string, logDir string) (string, error) {
	return logstore.ResolveViewFile(args, func() (string, error) {
		cfg, err := config.Resolve(config.Options{LogDir: logDir})
		if err != nil {
			return "", err
		}
		return cfg.LogDir, nil
	})
}

func runExport(args []string, logDir string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, exportUsage) }
	var last bool
	var command int
	var output string
	fs.BoolVar(&last, "last", false, "")
	fs.IntVar(&command, "command", 0, "")
	fs.StringVar(&output, "output", "", "")
	fs.StringVar(&output, "o", "", "")

	// Accept options both before and after the file, as users naturally type
	// `export session.log --command 2`. A -- delimiter protects filenames.
	var flags, files []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			if !hasValue && (name == "command" || name == "output" || name == "o") {
				if i+1 >= len(args) {
					return fmt.Errorf("%s requires a value", arg)
				}
				i++
				flags = append(flags, args[i])
			}
		} else {
			files = append(files, arg)
		}
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	var hasCommand bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "command" {
			hasCommand = true
		}
	})
	if hasCommand && command <= 0 {
		return fmt.Errorf("--command must be a positive command number")
	}
	if last {
		if len(files) != 0 {
			return fmt.Errorf("use either --last or a log file, not both")
		}
		files = []string{"--last"}
	}
	if len(files) != 1 {
		return fmt.Errorf("%s", strings.TrimSpace(exportUsage))
	}
	// ResolveViewFile treats a leading dash as an option; an explicit path
	// after export's -- delimiter is still a legitimate file.
	if strings.HasPrefix(files[0], "-") && !last {
		files[0] = "./" + files[0]
	}
	path, err := resolveLogArgument(files, logDir)
	if err != nil {
		return err
	}
	if output == "" || output == "-" {
		return viewer.Export(path, command, stdout)
	}
	if err := viewer.ExportToFile(path, command, output); err != nil {
		return err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		abs = output
	}
	fmt.Fprintf(stderr, "[kuroko] exported → %s\n", abs)
	return nil
}
