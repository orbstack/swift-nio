#!/bin/sh

killall -9 scon
exec ./scon "$@"
