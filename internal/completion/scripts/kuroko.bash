# bash completion for kuroko
#
# Install permanently:
#   kuroko completion bash > ~/.local/share/bash-completion/completions/kuroko
# Or load for the current shell only (source <(...) does not work in the
# bash 3.2 that macOS ships, so use eval):
#   eval "$(kuroko completion bash)"
#
# For a wrapped command (kuroko ssh ..., kuroko telnet ...), completion is
# handed to that command's own bash completion when bash-completion is
# loaded, so e.g. ssh hosts from ~/.ssh/config work as for plain ssh.
# Without bash-completion, kuroko offers ~/.ssh/config hosts for ssh and
# file names otherwise.

# _kuroko_reply PREFIX CUR WORDS sets COMPREPLY to PREFIX+word for each
# newline-separated word in WORDS that starts with CUR. Unlike compgen -W,
# words are compared literally and never expanded: they come from files
# (ssh_config hosts, log names), and "$(...)" in one must not run.
_kuroko_reply() {
    local prefix="$1" cur="$2" word
    COMPREPLY=()
    while IFS= read -r word; do
        [ -n "$word" ] || continue
        case "$word" in
        "$cur"*) COMPREPLY+=("${prefix}${word}") ;;
        esac
    done <<< "$3"
}

_kuroko() {
    local word cur cword ctx out first wrapped prefix=""
    word="${COMP_WORDS[COMP_CWORD]}"
    cur="$word"
    cword=$COMP_CWORD

    ctx=()
    if [ "$cword" -gt 1 ]; then
        ctx=("${COMP_WORDS[@]:1:cword-1}")
    fi

    # Completing the host of "user@host": with '@' in COMP_WORDBREAKS,
    # readline replaces the text from the '@' on (bash treats '@' as a
    # special prefix), so replies must start with "@". bash 4+ splits the
    # word into "user" "@" "ho" (just "user" "@" right after the '@'),
    # while bash 3.2 passes "user@ho" whole. Without '@' as a word break
    # (bash-completion 1.x removes it) the whole word is replaced.
    case "$COMP_WORDBREAKS" in
    *@*)
        if [ "$cword" -gt 1 ] && [ "$word" = "@" ]; then
            ctx+=("@")
            prefix="@" cur=""
        elif [ "$cword" -gt 2 ] && [ "${COMP_WORDS[cword-1]}" = "@" ]; then
            prefix="@"
        elif [ "${word#*@}" != "$word" ]; then
            prefix="@" cur="${word##*@}"
        fi
        ;;
    *)
        if [ "${word#*@}" != "$word" ]; then
            prefix="${word%@*}@" cur="${word##*@}"
        fi
        ;;
    esac

    out="$(kuroko __complete "${ctx[@]}" 2>/dev/null)"

    # A first line ":wrapped=N" means COMP_WORDS[N] is the wrapped command.
    first="${out%%$'\n'*}"
    case "$first" in
    :wrapped=*)
        wrapped="${first#:wrapped=}"
        if [ "$first" = "$out" ]; then
            out=""
        else
            out="${out#*$'\n'}"
        fi
        if declare -F _comp_command_offset >/dev/null 2>&1; then
            _comp_command_offset "$wrapped"
            return
        fi
        if declare -F _command_offset >/dev/null 2>&1; then
            _command_offset "$wrapped"
            return
        fi
        if [ -z "$out" ]; then
            compopt -o filenames 2>/dev/null
            COMPREPLY=($(compgen -f -- "${word}"))
            return
        fi
        _kuroko_reply "$prefix" "$cur" "$out"
        return
        ;;
    esac

    _kuroko_reply "" "$word" "$out"

    if [ "$cword" -eq 1 ]; then
        COMPREPLY+=($(compgen -c -- "${word}"))
    fi
}

complete -F _kuroko kuroko
