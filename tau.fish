# tau.fish — source this from ~/.config/fish/config.fish so that
#   tau workspace cd <id>
#   tau workspace add <dir> --cd
# change THIS shell's directory. A child process can't cd its parent, so the
# real work stays in the `tau` script and this wrapper just cds to what it prints.
#
#   echo "source $PWD/tau.fish" >> ~/.config/fish/config.fish   (run from the tau dir)

# Self-derive the script path from this file's location — portable, move-safe.
set -g __tau_bin (path resolve (path dirname (status --current-filename))/tau)

function tau
    if test (count $argv) -ge 2; and test "$argv[1]" = workspace
        switch "$argv[2]"
            case cd
                set -l dir (command $__tau_bin workspace cd $argv[3..-1]); or return
                cd $dir
                return
            case add
                if contains -- --cd $argv
                    set -l rest
                    for a in $argv[3..-1]
                        test "$a" = --cd; or set -a rest $a
                    end
                    # messages go to stderr (shown); the copy's path comes on stdout
                    set -l dir (command $__tau_bin workspace add $rest); or return
                    cd $dir
                    return
                end
        end
    end
    command $__tau_bin $argv
end
