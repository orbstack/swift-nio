#!/usr/bin/env bash

set -euxo pipefail

mkdir -p ../bins/out/completions/{bash,zsh,fish}

(exec -a orbctl ./scli completion bash) > ../bins/out/completions/bash/orbctl.bash
(exec -a orbctl ./scli completion zsh) > ../bins/out/completions/zsh/_orbctl
(exec -a orbctl ./scli completion fish) > ../bins/out/completions/fish/orbctl.fish

# only zsh needs a separate file for orb
(exec -a orb ./scli completion zsh) > ../bins/out/completions/zsh/_orb
