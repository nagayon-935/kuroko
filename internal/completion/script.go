package completion

import (
	_ "embed"
	"fmt"
	"io"
)

//go:embed scripts/kuroko.bash
var bashScript string

//go:embed scripts/kuroko.zsh
var zshScript string

//go:embed scripts/kuroko.fish
var fishScript string

// WriteScript writes the shell-completion script for shell to w. An empty
// shell defaults to bash; bash, zsh and fish are supported.
func WriteScript(w io.Writer, shell string) error {
	if shell == "" {
		shell = "bash"
	}
	switch shell {
	case "bash":
		_, err := io.WriteString(w, bashScript)
		return err
	case "zsh":
		_, err := io.WriteString(w, zshScript)
		return err
	case "fish":
		_, err := io.WriteString(w, fishScript)
		return err
	default:
		return fmt.Errorf("completion: unknown shell %q", shell)
	}
}
