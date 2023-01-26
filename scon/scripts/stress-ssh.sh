function testone() {
    seq=$RANDOM$RANDOM$RANDOM
    res="$(ssh -p 2222 ubuntu@localhost echo $seq)"
    #res="$(ssh localhost echo $seq)"
    if [ "$seq" != "$res" ]; then
        echo "ERROR: $seq != $res"
        exit 1
    fi
}

while true; do
    testone &
    sleep 0.005
done
