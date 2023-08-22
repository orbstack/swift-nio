#!/bin/sh

# wait for scon to exit cleanly
# otherwise forward state gets messed up
killall scon
while killall -0 scon; do
    sleep 0.1
done

exec ./scon "$@"
