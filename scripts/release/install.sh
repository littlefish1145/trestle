#!/usr/bin/env sh
set -eu

install_root=${TRESTLE_INSTALL_DIR:-"$HOME/.local/share/trestle/alpha"}
update_profile=1

while [ "$#" -gt 0 ]; do
  case "$1" in
    --install-dir)
      [ "$#" -ge 2 ] || { echo "--install-dir requires a path" >&2; exit 2; }
      install_root=$2
      shift 2
      ;;
    --no-profile)
      update_profile=0
      shift
      ;;
    -h|--help)
      echo "Usage: ./install.sh [--install-dir PATH] [--no-profile]"
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      exit 2
      ;;
  esac
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ -f "$script_dir/bin/trestle" ] || { echo "bin/trestle is missing from this package" >&2; exit 1; }

bin_dir=$install_root/bin
mkdir -p "$bin_dir"
cp "$script_dir/bin/trestle" "$bin_dir/trestle"
chmod 0755 "$bin_dir/trestle"

case ":${PATH:-}:" in
  *":$bin_dir:"*) path_ready=1 ;;
  *) path_ready=0 ;;
esac

if [ -n "${GITHUB_PATH:-}" ]; then
  if [ "$path_ready" -eq 0 ]; then
    printf '%s\n' "$bin_dir" >> "$GITHUB_PATH"
  fi
  echo "Installed Trestle in $bin_dir and added it to GITHUB_PATH."
elif [ "$update_profile" -eq 1 ] && [ "$path_ready" -eq 0 ]; then
  case ${SHELL:-} in
    */zsh) profile=${ZDOTDIR:-$HOME}/.zprofile ;;
    */bash)
      if [ -f "$HOME/.bash_profile" ]; then profile=$HOME/.bash_profile; else profile=$HOME/.profile; fi
      ;;
    *) profile=$HOME/.profile ;;
  esac
  marker="# Trestle Alpha"
  if ! grep -F "$bin_dir" "$profile" >/dev/null 2>&1; then
    {
      printf '\n%s\n' "$marker"
      printf 'export PATH="%s:$PATH"\n' "$bin_dir"
    } >> "$profile"
  fi
  echo "Installed Trestle in $bin_dir and added it to PATH through $profile."
  echo "Open a new shell, or run: export PATH=\"$bin_dir:\$PATH\""
else
  echo "Installed Trestle in $bin_dir."
  [ "$path_ready" -eq 1 ] || echo "Add this directory to PATH: $bin_dir"
fi
