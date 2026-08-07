# tau.bash — source this from your ~/.bashrc (bash) or ~/.zshrc (zsh) so that
#   tau workspace cd <name>
#   tau workspace add <dir> --cd
# change THIS shell's directory. A child process can't cd its parent, so the
# real work stays in the `tau` script and this wrapper just cds to what it prints.
#
#   echo "source $PWD/tau.bash" >> ~/.bashrc   (run from the tau dir, bash)
#   echo "source $PWD/tau.bash" >> ~/.zshrc    (run from the tau dir, zsh)
#
# fish users: see tau.fish instead.

# Resolve this file's dir portably: bash gives BASH_SOURCE, zsh falls back to $0
# (which is the sourced file's path under both shells).
__tau_bin="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)/tau"

tau() {
	if [ $# -ge 2 ] && [ "$1" = workspace ]; then
		case "$2" in
			cd)
				shift 2
				local dir
				dir="$(command "$__tau_bin" workspace cd "$@")" || return
				cd "$dir"
				return
				;;
			add)
				# Drop 'workspace add', strip a --cd flag if present, and if it was,
				# run the add and cd to the path it prints. Otherwise fall through.
				shift 2
				local has_cd=0 rest=() a
				for a in "$@"; do
					if [ "$a" = --cd ]; then has_cd=1; else rest+=("$a"); fi
				done
				if [ $has_cd -eq 1 ]; then
					local dir
					dir="$(command "$__tau_bin" workspace add "${rest[@]}")" || return
					cd "$dir"
					return
				fi
				set -- workspace add "$@"
				;;
		esac
	fi
	command "$__tau_bin" "$@"
}
