#compdef kuroko
#
# zsh completion for kuroko
#
# Load for the current shell (put in ~/.zshrc, after compinit):
#   eval "$(kuroko completion zsh)"
# Or install as a file named _kuroko in a directory on $fpath:
#   kuroko completion zsh > "${fpath[1]}/_kuroko"
#
# For a wrapped command (kuroko ssh ..., kuroko telnet ...), completion is
# handed to that command's own zsh completion, so ssh hosts and options
# complete as for plain ssh. kuroko's own subcommands, flags and log file
# names come from `kuroko __complete`.

_kuroko() {
    local out wrapped
    local -a cands

    # Words typed so far: everything between "kuroko" and the cursor.
    out="$(command kuroko __complete "${(@)words[2,CURRENT-1]}" 2>/dev/null)" || out=""
    cands=("${(@f)out}")

    # A first line ":wrapped=N" means words[N+1] is the wrapped command:
    # make it the command word and let zsh complete it as it would normally.
    if [[ "${cands[1]}" == :wrapped=* ]]; then
        wrapped="${cands[1]#:wrapped=}"
        if [[ "$wrapped" == <-> ]] && (( wrapped > 0 && wrapped < CURRENT )); then
            words=("${(@)words[wrapped+1,-1]}")
            (( CURRENT -= wrapped ))
            _normal
            return
        fi
        return 1
    fi

    # Complete the value of -d/--log-dir with directories (a wrapped command
    # keeps its own -d, so this only applies to kuroko's flag).
    case "${words[CURRENT-1]}" in
    -d | -log-dir | --log-dir)
        _files -/
        return
        ;;
    esac

    # Candidates are matched literally by compadd, never evaluated.
    cands=("${(@)cands:#}")
    (( ${#cands} )) && compadd -- "${cands[@]}"

    # Right after "kuroko", any command on PATH may be wrapped too.
    if (( CURRENT == 2 )); then
        _command_names -e
    fi
    return 0
}

# Run as a function when autoloaded from $fpath (_kuroko "$@" completes);
# register it when the file is sourced or eval'd instead.
if [[ "${funcstack[1]}" == _kuroko ]]; then
    _kuroko "$@"
else
    compdef _kuroko kuroko
fi
