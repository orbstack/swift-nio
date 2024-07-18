#!/bin/bash

# ========
# # symbolicate-panic.sh
# symbolicate a panic using lldb and a kdk
# requires lldb, otool, sed, awk, find
# requires curl, jq, to find right url for kdk
# ========

set -eufo pipefail

# ==== panic and kdk parsing ====

# args: panic_path
get_kdk_text_exec_base() {
  otool -l "$kdk_path" | grep -A 2 'segname __TEXT_EXEC' | grep 'vmaddr' | awk '{print $2}'
}

# args: kdk_path panic_text_exec_base address
get_symbol_from_address() {
  lldb "$1" -o "image lookup -a \`$3 - $2 + $(get_kdk_text_exec_base $1)\`" < /dev/null 2>&1 | awk '/Summary/{y=1}y' | sed 's/Summary://' | sed -r 's/^ *//g'
}

# args: panic_path
get_panic_text_exec_base() {
  sed -nr 's/Kernel text exec base:  (0x[0-9a-f])/\1/p' "$1"
}

# args: panic_path
get_array_panic_addresses() {
  local addrs
  addrs=($(sed -nr 's/.*Panicked thread: (0x[0-9a-f]*), backtrace: (0x[0-9a-f]*),.*/\1 \2/p' "$1"))
  addrs+=($(sed -nr 's/.*lr: (0x[0-9a-f]*).*/\1/p' "$1"))
  declare -p addrs | cut -d= -f2-
}

# ==== kdk selection, detection and installation ====
# args: panic_path
get_panic_build_version() {
  sed -nr 's/.*OS version: ([A-Z0-9]+)$/\1/p' $1
}

# args: panic_path
get_panic_kernel_version() {
  sed -nr 's|.*Kernel version: .*/RELEASE_[a-zA-Z0-9]+_(.+)$|\1|p' $1 | tr '[:upper:]' '[:lower:]'
}

# args: build_version kernel_version
try_find_kdk() {
  find /Library/Developer/KDKs/ -path "*/KDK_*_$1.kdk/System/Library/Kernels/kernel.release.$2.dSYM/Contents/Resources/DWARF/kernel.release.$2" | head -n1
}

# args: build_version
get_kdk_url() {
  local build_version os_version
  build_version="$1"
  os_version="$(curl -fsSL "https://api.appledb.dev/ios/macOS;${build_version}.json" | jq -r '.version')"
  echo "https://download.developer.apple.com/macOS/Kernel_Debug_Kit_${os_version}_build_${build_version}/Kernel_Debug_Kit_${os_version}_build_${build_version}.dmg"
}

# ==== main ====

print_help() {
  cat <<EOF
usage: symbolicate-panic.sh panic_path [kdk_path]

if a kdk is not provided, this script will try to find one or will give a download link
EOF
}

main() {
  if [ "$#" -lt "1" ]; then
    print_help
    exit 0
  fi

  local panic_path kdk_path
  panic_path="$1"
  kdk_path="${2:-}"
  
  if [ -z "$kdk_path" ]; then
    # no kdk_path was provided, search for one
    local build_ver kernel_ver
    build_ver="$(get_panic_build_version $panic_path)"
    kernel_ver="$(get_panic_kernel_version $panic_path)"

    kdk_path="$(try_find_kdk $build_ver $kernel_ver)"
    if [ -z "$kdk_path" ]; then
      # couldnt find a kdk, ask user to install it
      local kdk_url
      echo "unable to find a matching KDK."
      kdk_url="$(get_kdk_url $build_ver)"
      echo "visit https://developer.apple.com/download/all/ and then copy and paste $kdk_url into the address bar"
      exit 1
    fi
  fi

  panic_text_exec_base="$(get_panic_text_exec_base $panic_path)"
  eval declare -a addrs="$(get_array_panic_addresses $panic_path)"

  for addr in "${addrs[@]}"; do
    get_symbol_from_address $kdk_path $panic_text_exec_base $addr | xargs -I {} echo "$addr | {}"
  done
}

main "$@"
