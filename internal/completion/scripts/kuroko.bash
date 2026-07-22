# bash completion for kuroko
#
# Install permanently:
#   kuroko completion bash > ~/.local/share/bash-completion/completions/kuroko
# Or load for the current shell only:
#   source <(kuroko completion bash)

_kuroko() {
    local cur cword words ctx candidates
    cur="${COMP_WORDS[COMP_CWORD]}"
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD

    ctx=()
    if [ "$cword" -gt 1 ]; then
        ctx=("${words[@]:1:cword-1}")
    fi

    candidates="$(kuroko __complete "${ctx[@]}" 2>/dev/null)"
    COMPREPLY=($(compgen -W "${candidates}" -- "${cur}"))

    if [ "$cword" -eq 1 ]; then
        COMPREPLY+=($(compgen -c -- "${cur}"))
    fi
}

complete -F _kuroko kuroko
