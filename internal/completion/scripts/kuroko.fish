# fish completion for kuroko
#
# Load for the current shell:
#   kuroko completion fish | source
# Or install permanently:
#   kuroko completion fish > ~/.config/fish/completions/kuroko.fish
#
# For a wrapped command (kuroko ssh ..., kuroko telnet ...), completion is
# handed to fish's own completion of that command, so ssh hosts and options
# complete as for plain ssh. kuroko's own subcommands, flags and log file
# names come from `kuroko __complete`.

function __kuroko_complete
    # Tokens before the cursor, starting with "kuroko" itself.
    set -l tokens (commandline -opc)
    set -l cur (commandline -ct)

    # Words typed so far, as `kuroko __complete` expects (no "kuroko").
    # Dropping the first element avoids an out-of-range slice when only
    # "kuroko" has been typed.
    set -l ctx $tokens
    set -e ctx[1]
    set -l out (command kuroko __complete $ctx 2>/dev/null)

    # `out` is empty when kuroko fails or has nothing to offer, so never index
    # it blindly.
    set -l first ''
    set -q out[1]; and set first $out[1]

    # A first line ":wrapped=N" means the wrapped command is word N after
    # "kuroko" (fish arrays are 1-based, so tokens[N+1]). The test below
    # guarantees N+1 is within tokens.
    if string match -q -r -- '^:wrapped=[0-9]+$' "$first"
        set -l n (string replace -- ':wrapped=' '' "$first")
        if test $n -ge 1 -a $n -lt (count $tokens)
            set -l line (string join ' ' -- (string escape -- $tokens[(math $n + 1)..-1]))
            complete -C "$line $cur"
        end
        return
    end

    # The value of -d/--log-dir is a directory.
    if test (count $tokens) -ge 2
        switch $tokens[-1]
            case -d -log-dir --log-dir
                __fish_complete_directories "$cur"
                return
        end
    end

    # printf with an empty list would print one empty candidate.
    if set -q out[1]
        printf '%s\n' $out
    end

    # Right after "kuroko", any command on PATH may be wrapped too.
    if test (count $tokens) -eq 1; and test -n "$cur"
        complete -C "$cur"
    end
end

complete -c kuroko -f -a '(__kuroko_complete)'
