#!/bin/sh

set -euo pipefail

mount -t tracefs tracefs /sys/kernel/tracing

exec bpftrace "$1.bt"
