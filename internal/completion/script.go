package completion

import (
	_ "embed"
	"fmt"
	"io"
)

//go:embed scripts/kuroko.bash
var bashScript string

// WriteScript writes the shell-completion script for shell to w. An empty
// shell defaults to bash. Only bash is currently supported; zsh and fish
// are recognized subcommand values (see Candidates) but not yet implemented.
func WriteScript(w io.Writer, shell string) error {
	if shell == "" {
		shell = "bash"
	}
	switch shell {
	case "bash":
		_, err := io.WriteString(w, bashScript)
		return err
	case "zsh", "fish":
		return fmt.Errorf("completion: %s is not yet supported", shell)
	default:
		return fmt.Errorf("completion: unknown shell %q", shell)
	}
}
